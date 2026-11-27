package core

import (
	"encoding/json"
	"reflect"
	"testing"
)

func decodeSchema(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSanitizeToolSchemaDropsUnknownKeywords(t *testing.T) {
	in := decodeSchema(t, `{"type":"object","returnDirect":false,"properties":{"returnDirect":{"type":"boolean","returnDirect":true},"list":{"type":"array","items":{"type":"string","x-extra":1},"default":["a"]}},"required":["list"],"anyOf":[{"type":"object","foo":1}],"additionalProperties":{"type":"string","bar":2}}`)
	want := decodeSchema(t, `{"type":"object","properties":{"returnDirect":{"type":"boolean"},"list":{"type":"array","items":{"type":"string"},"default":["a"]}},"required":["list"],"anyOf":[{"type":"object"}],"additionalProperties":{"type":"string"}}`)
	if got := SanitizeToolSchema(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if _, ok := in.(map[string]any)["returnDirect"]; !ok {
		t.Fatal("input schema was mutated")
	}
}

func TestSanitizeToolSchemaKeepsValidKeywords(t *testing.T) {
	raw := `{"type":"object","returnDirect":false,"$defs":{"id":{"$anchor":"id","type":"string","minLength":1}},"properties":{"id":{"$ref":"#id"},"a":{"type":"string"},"b":{"type":"string"},"tags":{"type":"array","contains":{"type":"string","pattern":"^t"},"minContains":1,"unevaluatedItems":false},"pair":{"type":"array","items":[{"type":"string"},{"type":"integer"}]}},"dependentRequired":{"a":["b"]},"dependentSchemas":{"b":{"required":["a"]}},"propertyNames":{"pattern":"^[a-z]+$"},"unevaluatedProperties":false}`
	want := decodeSchema(t, raw)
	delete(want.(map[string]any), "returnDirect")
	if got := SanitizeToolSchema(decodeSchema(t, raw)); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
