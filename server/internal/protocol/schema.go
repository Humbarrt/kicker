package protocol

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

// SchemaDialect is the JSON Schema version of the generated schemas.
const SchemaDialect = "https://json-schema.org/draft/2020-12/schema"

// Schema returns the JSON Schema of a Go type (a value or a reflect.Type), following the
// same field rules as the decoder: no omitempty = required, enum/min/max/doc tags.
func Schema(v any) map[string]any {
	t, ok := v.(reflect.Type)
	if !ok {
		t = reflect.TypeOf(v)
	}
	return schemaOf(t, reflect.StructTag(""))
}

func schemaOf(t reflect.Type, tag reflect.StructTag) map[string]any {
	s := map[string]any{}
	if doc := tag.Get("doc"); doc != "" {
		s["description"] = doc
	}
	if t == rawMessageType {
		return s // any JSON value
	}
	nullable := false
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
		nullable = true
	}
	typeName := ""
	switch t.Kind() {
	case reflect.String:
		typeName = "string"
		if enum := tag.Get("enum"); enum != "" {
			s["enum"] = strings.Split(enum, "|")
		}
	case reflect.Bool:
		typeName = "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		typeName = "integer"
		if t.Kind() >= reflect.Uint && t.Kind() <= reflect.Uint64 {
			s["minimum"] = 0
		}
		if t.Kind() == reflect.Uint32 {
			s["maximum"] = uint64(^uint32(0))
		}
		if lo, err := strconv.ParseInt(tag.Get("min"), 10, 64); err == nil {
			s["minimum"] = lo
		}
		if hi, err := strconv.ParseInt(tag.Get("max"), 10, 64); err == nil {
			s["maximum"] = hi
		}
	case reflect.Float32, reflect.Float64:
		typeName = "number"
	case reflect.Slice:
		typeName = "array"
		s["items"] = schemaOf(t.Elem(), "")
	case reflect.Array:
		typeName = "array"
		s["items"] = schemaOf(t.Elem(), "")
		s["minItems"] = t.Len()
		s["maxItems"] = t.Len()
	case reflect.Map:
		typeName = "object"
		s["additionalProperties"] = schemaOf(t.Elem(), "")
	case reflect.Struct:
		typeName = "object"
		props := map[string]any{}
		required := []string{}
		for _, fi := range jsonFields(t) {
			sf := t.Field(fi.index)
			props[fi.name] = schemaOf(sf.Type, sf.Tag)
			if !fi.omitempty {
				required = append(required, fi.name)
			}
		}
		s["properties"] = props
		if len(required) > 0 {
			s["required"] = required
		}
	}
	if typeName != "" {
		if nullable {
			s["type"] = []string{typeName, "null"}
		} else {
			s["type"] = typeName
		}
	}
	return s
}

// MessageSchemas returns one schema per message type, keyed by type and direction,
// e.g. "evt" or "ack (module → server)".
func MessageSchemas() map[string]any {
	out := map[string]any{}
	for t, entry := range messageTypes {
		s := Schema(reflect.TypeOf(entry.new()).Elem())
		props := s["properties"].(map[string]any)
		props["t"] = map[string]any{"const": string(t)}
		out[string(t)] = s
	}
	return out
}

// CapabilitySchema returns one JSON Schema document for a catalog capability,
// with its params, config, events and commands as $defs.
func CapabilitySchema(c *Capability) map[string]any {
	defs := map[string]any{}
	add := func(name string, typ any) {
		if typ == nil {
			defs[name] = map[string]any{"description": "no data"}
			return
		}
		defs[name] = Schema(typ)
	}
	if c.Params != nil {
		add("params", c.Params)
	}
	if c.Config != nil {
		add("config", c.Config)
	}
	for _, name := range c.EventNames() {
		add("event."+name, c.Events[name])
	}
	for _, name := range c.CommandNames() {
		add("command."+name, c.Commands[name])
	}
	doc := map[string]any{
		"$schema":     SchemaDialect,
		"$id":         "kicker:capability/" + c.Key(),
		"title":       c.Key(),
		"description": c.Doc,
		"$defs":       defs,
	}
	if len(c.Roles) > 0 {
		doc["x-roles"] = c.Roles
	}
	return doc
}

// ProtocolSchema returns the JSON Schema document of all protocol messages.
func ProtocolSchema() map[string]any {
	return map[string]any{
		"$schema":     SchemaDialect,
		"$id":         "kicker:protocol/v" + strconv.Itoa(Version),
		"title":       "kicker module protocol v" + strconv.Itoa(Version),
		"description": "One JSON object per line, max " + strconv.Itoa(MaxLine) + " bytes including the newline.",
		"$defs":       MessageSchemas(),
	}
}

// MarshalSchema renders a schema document deterministically (sorted keys, indented).
func MarshalSchema(doc map[string]any) ([]byte, error) {
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
