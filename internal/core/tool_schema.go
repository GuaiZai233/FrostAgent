package core

// toolSchemaKeywords lists the JSON Schema keywords kept in tool parameter
// schemas. Providers and relays that translate schemas into a strict format
// (for example Gemini via Antigravity) reject unknown fields such as
// "returnDirect", which some MCP servers attach to their input schemas.
var toolSchemaKeywords = map[string]bool{
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

// SanitizeToolSchema returns a copy of a tool parameter schema without
// non-standard keywords. Property names and literal values are preserved.
func SanitizeToolSchema(value any) any {
	schema, ok := value.(map[string]any)
	if !ok {
		return value
	}
	out := make(map[string]any, len(schema))
	for key, child := range schema {
		if !toolSchemaKeywords[key] {
			continue
		}
		switch key {
		case "properties", "patternProperties", "$defs", "definitions":
			if named, ok := child.(map[string]any); ok {
				cleaned := make(map[string]any, len(named))
				for name, sub := range named {
					cleaned[name] = SanitizeToolSchema(sub)
				}
				out[key] = cleaned
				continue
			}
		case "items", "additionalProperties", "not", "if", "then", "else":
			out[key] = SanitizeToolSchema(child)
			continue
		case "prefixItems", "anyOf", "oneOf", "allOf":
			if list, ok := child.([]any); ok {
				cleaned := make([]any, len(list))
				for i, sub := range list {
					cleaned[i] = SanitizeToolSchema(sub)
				}
				out[key] = cleaned
				continue
			}
		}
		out[key] = child
	}
	return out
}
