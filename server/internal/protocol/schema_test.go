package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSchemaRequiredAndConstraints(t *testing.T) {
	s := Schema(Evt{})
	req := s["required"].([]string)
	want := []string{"t", "i", "e", "n", "ts"}
	if !reflect.DeepEqual(req, want) {
		t.Errorf("required = %v, want %v", req, want)
	}
	n := s["properties"].(map[string]any)["n"].(map[string]any)
	if n["type"] != "integer" || n["minimum"] != 0 || n["maximum"] != uint64(4294967295) {
		t.Errorf("n schema = %v", n)
	}

	st := Schema(DisplayState{})
	props := st["properties"].(map[string]any)
	if s := props["s"].(map[string]any); s["minItems"] != 2 || s["maxItems"] != 2 {
		t.Errorf("s must be exactly 2 items: %v", s)
	}
	if ph := props["ph"].(map[string]any); len(ph["enum"].([]string)) != 5 {
		t.Errorf("ph enum: %v", ph)
	}
	img := Schema(DisplayPlayer{})["properties"].(map[string]any)["img"].(map[string]any)
	if !reflect.DeepEqual(img["type"], []string{"string", "null"}) {
		t.Errorf("img must be nullable string: %v", img)
	}
}

func TestCapabilitySchemaDocs(t *testing.T) {
	for _, c := range Catalog {
		doc := CapabilitySchema(c)
		b, err := MarshalSchema(doc)
		if err != nil {
			t.Fatalf("%s: %v", c.Key(), err)
		}
		var back map[string]any
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("%s: not valid JSON: %v", c.Key(), err)
		}
		defs := back["$defs"].(map[string]any)
		for _, e := range c.EventNames() {
			if _, ok := defs["event."+e]; !ok {
				t.Errorf("%s: missing event.%s", c.Key(), e)
			}
		}
	}
	if _, err := MarshalSchema(ProtocolSchema()); err != nil {
		t.Fatal(err)
	}
}

// The committed schemas in /catalog must match the Go types. Run `go generate ./...` to update.
func TestCatalogUpToDate(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "catalog")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("catalog folder not found")
	}
	check := func(name string, doc map[string]any) {
		want, _ := MarshalSchema(doc)
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != string(want) {
			t.Errorf("catalog/%s is missing or outdated – run `go generate ./...` in server/", name)
		}
	}
	check("protocol-v1.schema.json", ProtocolSchema())
	for _, c := range Catalog {
		check(c.Key()+".schema.json", CapabilitySchema(c))
	}
}
