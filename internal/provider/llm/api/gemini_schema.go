package api

// geminiSchemaKeywords lists the JSON Schema keywords forwarded to Gemini.
// Relays that translate parametersJsonSchema into the strict Schema proto
// (for example Antigravity) reject unknown fields such as "returnDirect",
// which some MCP servers attach to their input schemas.
var geminiSchemaKeywords = map[string]bool{
	"$schema": true, "$id": true, "$ref": true, "$defs": true, "definitions": true,
	"type": true, "format": true, "title": true, "description": true,
	"enum": true, "const": true, "default": true, "examples": true,
	"properties": true, "required": true, "additionalProperties": true, "patternProperties": true,
	"items": true, "prefixItems": true, "minItems": true, "maxItems": true, "uniqueItems": true,
	"minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minLength": true, "maxLength": true, "pattern": true,
	"minProperties": true, "maxProperties": true,
	"anyOf": true, "oneOf": true, "allOf": true, "not": true,
	"if": true, "then": true, "else": true, "nullable": true, "propertyOrdering": true,
}

// sanitizeGeminiSchema returns a copy of a tool parameter schema without
// non-standard keywords. Property names and literal values are preserved.
func sanitizeGeminiSchema(value any) any {
	schema, ok := value.(map[string]any)
	if !ok {
		return value
	}
	out := make(map[string]any, len(schema))
	for key, child := range schema {
		if !geminiSchemaKeywords[key] {
			continue
		}
		switch key {
		case "properties", "patternProperties", "$defs", "definitions":
			if named, ok := child.(map[string]any); ok {
				cleaned := make(map[string]any, len(named))
				for name, sub := range named {
					cleaned[name] = sanitizeGeminiSchema(sub)
				}
				out[key] = cleaned
				continue
			}
		case "items", "additionalProperties", "not", "if", "then", "else":
			out[key] = sanitizeGeminiSchema(child)
			continue
		case "prefixItems", "anyOf", "oneOf", "allOf":
			if list, ok := child.([]any); ok {
				cleaned := make([]any, len(list))
				for i, sub := range list {
					cleaned[i] = sanitizeGeminiSchema(sub)
				}
				out[key] = cleaned
				continue
			}
		}
		out[key] = child
	}
	return out
}
