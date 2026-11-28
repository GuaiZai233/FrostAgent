package core

type schemaKeywordKind int

const (
	kindLiteral      schemaKeywordKind = iota // value is data, kept verbatim
	kindSchema                                // value is one subschema
	kindSchemaMap                             // value maps names to subschemas
	kindSchemaList                            // value is a list of subschemas
	kindSchemaOrList                          // value is a subschema or a list of them
)

// toolSchemaKeywords lists the JSON Schema keywords (Draft-07 and Draft 2020-12,
// plus the Gemini/OpenAPI "nullable" and "propertyOrdering" hints) kept in tool
// parameter schemas. Providers and relays that translate schemas into a strict
// format (for example Gemini via Antigravity) reject unknown fields such as
// "returnDirect", which some MCP servers attach to their input schemas.
var toolSchemaKeywords = map[string]schemaKeywordKind{
	// Core and metadata.
	"$schema": kindLiteral, "$id": kindLiteral, "$ref": kindLiteral, "$anchor": kindLiteral,
	"$dynamicRef": kindLiteral, "$dynamicAnchor": kindLiteral, "$recursiveRef": kindLiteral,
	"$recursiveAnchor": kindLiteral, "$vocabulary": kindLiteral, "$comment": kindLiteral,
	"$defs": kindSchemaMap, "definitions": kindSchemaMap,
	"title": kindLiteral, "description": kindLiteral, "default": kindLiteral, "examples": kindLiteral,
	"deprecated": kindLiteral, "readOnly": kindLiteral, "writeOnly": kindLiteral,
	"nullable": kindLiteral, "propertyOrdering": kindLiteral,
	// Validation.
	"type": kindLiteral, "enum": kindLiteral, "const": kindLiteral, "format": kindLiteral,
	"multipleOf": kindLiteral, "maximum": kindLiteral, "exclusiveMaximum": kindLiteral,
	"minimum": kindLiteral, "exclusiveMinimum": kindLiteral,
	"maxLength": kindLiteral, "minLength": kindLiteral, "pattern": kindLiteral,
	"maxItems": kindLiteral, "minItems": kindLiteral, "uniqueItems": kindLiteral,
	"maxContains": kindLiteral, "minContains": kindLiteral,
	"maxProperties": kindLiteral, "minProperties": kindLiteral,
	"required": kindLiteral, "dependentRequired": kindLiteral,
	"contentEncoding": kindLiteral, "contentMediaType": kindLiteral,
	// Applicators.
	"properties": kindSchemaMap, "patternProperties": kindSchemaMap, "dependentSchemas": kindSchemaMap,
	"dependencies":         kindSchemaMap, // Draft-07: values may also be name lists, kept verbatim.
	"additionalProperties": kindSchema, "unevaluatedProperties": kindSchema, "propertyNames": kindSchema,
	"items": kindSchemaOrList, "additionalItems": kindSchema, "unevaluatedItems": kindSchema,
	"contains": kindSchema, "contentSchema": kindSchema,
	"prefixItems": kindSchemaList, "allOf": kindSchemaList, "anyOf": kindSchemaList, "oneOf": kindSchemaList,
	"not": kindSchema, "if": kindSchema, "then": kindSchema, "else": kindSchema,
}

// SanitizeToolSchema returns a copy of a tool parameter schema without
// non-standard keywords, recursing into every schema-bearing keyword. Property
// names and literal values (enum, const, default, ...) are preserved.
func SanitizeToolSchema(value any) any {
	schema, ok := value.(map[string]any)
	if !ok {
		return value
	}
	out := make(map[string]any, len(schema))
	for key, child := range schema {
		kind, known := toolSchemaKeywords[key]
		if !known {
			continue
		}
		out[key] = sanitizeKeywordValue(kind, child)
	}
	return out
}

func sanitizeKeywordValue(kind schemaKeywordKind, child any) any {
	switch kind {
	case kindSchema:
		return SanitizeToolSchema(child)
	case kindSchemaMap:
		if named, ok := child.(map[string]any); ok {
			cleaned := make(map[string]any, len(named))
			for name, sub := range named {
				cleaned[name] = SanitizeToolSchema(sub)
			}
			return cleaned
		}
	case kindSchemaList:
		return sanitizeSchemaList(child)
	case kindSchemaOrList:
		if _, ok := child.([]any); ok {
			return sanitizeSchemaList(child)
		}
		return SanitizeToolSchema(child)
	}
	return child
}

func sanitizeSchemaList(child any) any {
	list, ok := child.([]any)
	if !ok {
		return child
	}
	cleaned := make([]any, len(list))
	for i, sub := range list {
		cleaned[i] = SanitizeToolSchema(sub)
	}
	return cleaned
}
