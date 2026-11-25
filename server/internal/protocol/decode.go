package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

// Decode decodes one JSON line without checking the direction.
func Decode(line []byte) (Message, []string, error) {
	return decode(line, nil)
}

// DecodeFrom decodes one JSON line that was sent in direction dir.
// Warnings (e.g. unknown fields) don't make the message invalid but belong in the module console.
// The returned error is always an *Error that can be sent back as an `err` message.
func DecodeFrom(dir Direction, line []byte) (Message, []string, error) {
	return decode(line, &dir)
}

func decode(line []byte, dir *Direction) (Message, []string, error) {
	if !json.Valid(line) {
		return nil, nil, errf(ErrBadJSON, "not valid JSON")
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(line, &keys); err != nil {
		return nil, nil, errf(ErrSchema, "message must be a JSON object")
	}
	// id or n, if readable, so errors can reference the message they belong to.
	var ref *uint32
	for _, k := range []string{"id", "n"} {
		var n uint32
		if raw, ok := keys[k]; ok && json.Unmarshal(raw, &n) == nil {
			ref = &n
			break
		}
	}
	withRef := func(e *Error) *Error { e.Ref = ref; return e }

	rawT, ok := keys["t"]
	if !ok {
		return nil, nil, withRef(errf(ErrSchema, "missing field t"))
	}
	var t Type
	if err := json.Unmarshal(rawT, &t); err != nil {
		return nil, nil, withRef(errf(ErrSchema, "field t must be a string"))
	}
	entry, ok := messageTypes[t]
	if !ok || (dir != nil && !slices.Contains(entry.dirs, *dir)) {
		return nil, nil, withRef(errf(ErrUnknownType, "unknown message type %q", t))
	}

	msg := entry.new()
	warnings, perr := decodeStrict(line, msg)
	if perr != nil {
		return nil, warnings, withRef(perr)
	}
	if s, ok := msg.(*Simple); ok && s.T != t {
		return nil, warnings, withRef(errf(ErrSchema, "field t: unexpected value %q", s.T))
	}
	if v, ok := msg.(validator); ok {
		if perr := v.validate(); perr != nil {
			return nil, warnings, withRef(perr)
		}
	}
	if a, ok := msg.(*Ack); ok && dir != nil {
		if *dir == ModuleToServer && a.ID == nil {
			return nil, warnings, errf(ErrSchema, "ack from a module needs id")
		}
		if *dir == ServerToModule && a.N == nil {
			return nil, warnings, errf(ErrSchema, "ack from the server needs n")
		}
	}
	return msg, warnings, nil
}

// DecodePayload strictly decodes a `d` payload (event data, command args, config) into v,
// applying the same field rules as for messages. An empty payload is treated as {}.
func DecodePayload(d json.RawMessage, v any) ([]string, error) {
	if len(bytes.TrimSpace(d)) == 0 || bytes.Equal(bytes.TrimSpace(d), []byte("null")) {
		d = json.RawMessage("{}")
	}
	warnings, perr := decodeStrict(d, v)
	if perr != nil {
		return warnings, perr
	}
	return warnings, nil
}

// decodeStrict unmarshals raw into v (a pointer to a struct) and then checks the field rules:
// required keys, null values, enums, min/max, and collects warnings for unknown keys.
func decodeStrict(raw []byte, v any) ([]string, *Error) {
	if err := json.Unmarshal(raw, v); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			field := typeErr.Field
			if field == "" {
				return nil, errf(ErrSchema, "expected a JSON object")
			}
			return nil, errf(ErrSchema, "field %s: expected %s, got %s", field, kindName(typeErr.Type), typeErr.Value)
		}
		var syntaxErr *json.SyntaxError
		if errors.As(err, &syntaxErr) {
			return nil, errf(ErrBadJSON, "%v", err)
		}
		return nil, errf(ErrSchema, "%v", err)
	}
	var warnings []string
	perr := checkObject(raw, reflect.ValueOf(v).Elem(), "", &warnings)
	return warnings, perr
}

type fieldInfo struct {
	name      string
	omitempty bool
	index     int
}

func jsonFields(t reflect.Type) []fieldInfo {
	var out []fieldInfo
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" || !f.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		out = append(out, fieldInfo{name: name, omitempty: strings.Contains(opts, "omitempty"), index: i})
	}
	return out
}

func checkObject(raw []byte, v reflect.Value, path string, warnings *[]string) *Error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return errf(ErrSchema, "%s: expected an object", orRoot(path))
	}
	t := v.Type()
	known := make(map[string]bool)
	for _, fi := range jsonFields(t) {
		known[fi.name] = true
		sf := t.Field(fi.index)
		fv := v.Field(fi.index)
		fpath := join(path, fi.name)
		val, present := keys[fi.name]
		isNull := present && bytes.Equal(bytes.TrimSpace(val), []byte("null"))

		if !present {
			if !fi.omitempty {
				return errf(ErrSchema, "missing field %s", fpath)
			}
			continue
		}
		if isNull {
			if sf.Type.Kind() != reflect.Pointer && !fi.omitempty {
				return errf(ErrSchema, "field %s must not be null", fpath)
			}
			continue
		}
		if perr := checkValue(val, fv, sf, fpath, warnings); perr != nil {
			return perr
		}
	}
	for k := range keys {
		if !known[k] {
			*warnings = append(*warnings, fmt.Sprintf("unknown field %s", join(path, k)))
		}
	}
	slices.Sort(*warnings)
	return nil
}

func checkValue(raw []byte, v reflect.Value, sf reflect.StructField, path string, warnings *[]string) *Error {
	if v.Type() == rawMessageType {
		return nil
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String:
		if enum := sf.Tag.Get("enum"); enum != "" && !slices.Contains(strings.Split(enum, "|"), v.String()) {
			return errf(ErrSchema, "field %s: %q is not one of %s", path, v.String(), enum)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return checkRange(v.Int(), sf, path)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return checkRange(int64(v.Uint()), sf, path)
	case reflect.Struct:
		return checkObject(raw, v, path, warnings)
	case reflect.Slice, reflect.Array:
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return errf(ErrSchema, "field %s: expected an array", path)
		}
		if v.Kind() == reflect.Array && len(items) != v.Len() {
			return errf(ErrSchema, "field %s: expected exactly %d items, got %d", path, v.Len(), len(items))
		}
		if v.Type().Elem().Kind() == reflect.Struct {
			for i, item := range items {
				if perr := checkObject(item, v.Index(i), fmt.Sprintf("%s[%d]", path, i), warnings); perr != nil {
					return perr
				}
			}
		}
	}
	return nil
}

func checkRange(n int64, sf reflect.StructField, path string) *Error {
	if s := sf.Tag.Get("min"); s != "" {
		if lo, _ := strconv.ParseInt(s, 10, 64); n < lo {
			return errf(ErrSchema, "field %s: %d is below the minimum %d", path, n, lo)
		}
	}
	if s := sf.Tag.Get("max"); s != "" {
		if hi, _ := strconv.ParseInt(s, 10, 64); n > hi {
			return errf(ErrSchema, "field %s: %d is above the maximum %d", path, n, hi)
		}
	}
	return nil
}

func kindName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "non-negative integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Struct, reflect.Map:
		return "object"
	}
	return t.String()
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func orRoot(path string) string {
	if path == "" {
		return "message"
	}
	return path
}
