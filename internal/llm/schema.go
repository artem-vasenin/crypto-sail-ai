package llm

// ValidateJSONSchema implements the intentionally small subset of JSON Schema
// used by our two *static* llama.cpp response contracts. The server-side
// response_format is NOT a security boundary: validate again inside Go.
// Unsupported schema constructs fail closed instead of silently being ignored.
import (
	"encoding/json"
	"errors"
	"fmt"
)

func ValidateJSONSchema(raw []byte, schema map[string]any) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return validateNode(value, schema, "$", 0)
}

func validateNode(v any, schema map[string]any, path string, depth int) error {
	if depth > 16 {
		return errors.New("JSON Schema nesting limit exceeded")
	}
	typ, ok := schema["type"].(string)
	if !ok {
		return fmt.Errorf("%s: missing schema type", path)
	}
	switch typ {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected object", path)
		}
		p, ok := schema["properties"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: invalid properties schema", path)
		}
		required, ok := schema["required"].([]string)
		if !ok {
			return fmt.Errorf("%s: missing required schema", path)
		}
		for _, k := range required {
			if _, present := obj[k]; !present {
				return fmt.Errorf("%s: required field %q missing", path, k)
			}
		}
		for k, val := range obj {
			child, known := p[k]
			if !known {
				if deny, ok := schema["additionalProperties"].(bool); !ok || !deny {
					return fmt.Errorf("%s: unexpected field %q", path, k)
				}
				continue
			}
			childSchema, ok := child.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s: invalid child schema", path, k)
			}
			if err := validateNode(val, childSchema, path+"."+k, depth+1); err != nil {
				return err
			}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s: expected array", path)
		}
		if min, ok := schema["minItems"].(int); ok && len(arr) < min {
			return fmt.Errorf("%s: too few items", path)
		}
		child, ok := schema["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: invalid items schema", path)
		}
		for i, x := range arr {
			if err := validateNode(x, child, fmt.Sprintf("%s[%d]", path, i), depth+1); err != nil {
				return err
			}
		}
	case "string":
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s: expected string", path)
		}
		if allowed, ok := schema["enum"].([]string); ok {
			present := false
			for _, item := range allowed {
				if s == item {
					present = true
					break
				}
			}
			if !present {
				return fmt.Errorf("%s: unrecognized enum value", path)
			}
		}
	case "number":
		if _, ok := v.(float64); !ok {
			return fmt.Errorf("%s: expected number", path)
		}
	default:
		return fmt.Errorf("%s: unsupported schema type %q", path, typ)
	}
	return nil
}
