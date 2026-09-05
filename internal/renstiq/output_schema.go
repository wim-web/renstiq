package renstiq

import (
	"encoding/json"
	"reflect"
	"strings"
)

// Output schemas are derived from the actual wire types to prevent drift.
func outputSchema(v any) ([]byte, error) {
	schema := wireSchema(reflect.TypeOf(v))
	schema["$schema"] = "http://json-schema.org/draft-07/schema#"
	return json.MarshalIndent(schema, "", "  ")
}
func wireSchema(t reflect.Type) map[string]any {
	if t == reflect.TypeOf(json.RawMessage{}) {
		return map[string]any{}
	}
	if t.Kind() == reflect.Pointer {
		return map[string]any{"anyOf": []any{wireSchema(t.Elem()), map[string]any{"type": "null"}}}
	}
	switch t.Kind() {
	case reflect.Struct:
		props := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			name := tag[0]
			if name == "" {
				name = f.Name
			}
			props[name] = wireSchema(f.Type)
			if len(tag) == 1 {
				required = append(required, name)
			}
		}
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	case reflect.Slice:
		return map[string]any{"type": []string{"array", "null"}, "items": wireSchema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": []string{"object", "null"}, "additionalProperties": wireSchema(t.Elem())}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		return map[string]any{}
	}
}
