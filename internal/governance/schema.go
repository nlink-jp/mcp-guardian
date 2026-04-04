package governance

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SchemaValidationResult holds the result of schema validation.
type SchemaValidationResult struct {
	Valid    bool
	Errors   []string
}

// ValidateSchema validates tool arguments against an inputSchema.
// Checks required fields and basic type matching.
func ValidateSchema(args map[string]interface{}, schema json.RawMessage) SchemaValidationResult {
	if len(schema) == 0 {
		return SchemaValidationResult{Valid: true}
	}

	var s struct {
		Type       string                            `json:"type"`
		Required   []string                          `json:"required"`
		Properties map[string]map[string]interface{} `json:"properties"`
	}
	if json.Unmarshal(schema, &s) != nil {
		return SchemaValidationResult{Valid: true} // can't parse, skip
	}

	var errors []string

	// Check required fields
	for _, req := range s.Required {
		if _, ok := args[req]; !ok {
			errors = append(errors, fmt.Sprintf("missing required field: %s", req))
		}
	}

	// Basic type checking
	for name, prop := range s.Properties {
		val, exists := args[name]
		if !exists {
			continue
		}
		if expectedType, ok := prop["type"].(string); ok {
			if !checkType(val, expectedType) {
				errors = append(errors, fmt.Sprintf("field %s: expected %s", name, expectedType))
			}
		}
	}

	return SchemaValidationResult{
		Valid:  len(errors) == 0,
		Errors: errors,
	}
}

func checkType(val interface{}, expected string) bool {
	switch expected {
	case "string":
		_, ok := val.(string)
		return ok
	case "number", "integer":
		_, ok := val.(float64)
		return ok
	case "boolean":
		_, ok := val.(bool)
		return ok
	case "array":
		_, ok := val.([]interface{})
		return ok
	case "object":
		_, ok := val.(map[string]interface{})
		return ok
	}
	return true
}

// FormatSchemaErrors formats validation errors into a single string.
func FormatSchemaErrors(errors []string) string {
	return strings.Join(errors, "; ")
}
