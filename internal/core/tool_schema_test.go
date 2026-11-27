package core

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSanitizeToolSchemaDropsUnknownKeywords(t *testing.T) {
	raw := `{"type":"object","returnDirect":false,"properties":{"returnDirect":{"type":"boolean","returnDirect":true},"list":{"type":"array","items":{"type":"string","x-extra":1},"default":["a"]}},"required":["list"],"anyOf":[{"type":"object","foo":1}],"additionalProperties":{"type":"string","bar":2}}`
	want := `{"type":"object","properties":{"returnDirect":{"type":"boolean"},"list":{"type":"array","items":{"type":"string"},"default":["a"]}},"required":["list"],"anyOf":[{"type":"object"}],"additionalProperties":{"type":"string"}}`
	var in, exp any
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &exp); err != nil {
		t.Fatal(err)
	}
	if got := SanitizeToolSchema(in); !reflect.DeepEqual(got, exp) {
		t.Fatalf("got %v want %v", got, exp)
	}
	if _, ok := in.(map[string]any)["returnDirect"]; !ok {
		t.Fatal("input schema was mutated")
	}
}
