package renstiq

import (
	"encoding/json"
	"reflect"
	"strings"
)

// Output schemas are derived from the actual wire types to prevent drift.
func outputSchema(v any) ([]byte, error) {
	schema := wireSchema(reflect.TypeOf(v))
	schema["properties"].(map[string]any)["version"] = map[string]any{"const": 1}
	schema["properties"].(map[string]any)["error"] = map[string]any{"type": "string"}
	failure := wireSchema(reflect.TypeOf(ErrorResult{}))
	failure["properties"].(map[string]any)["version"] = map[string]any{"const": 1}
	return json.MarshalIndent(map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"oneOf":   []any{schema, failure},
	}, "", "  ")
}
func wireSchema(t reflect.Type) map[string]any {
	if t == reflect.TypeOf(SelectionStatus("")) {
		return map[string]any{"type": "string", "enum": []SelectionStatus{SelectionCandidate, SelectionExcluded, SelectionUnknown}, "description": "candidate means not mechanically excluded, not permission to merge; unknown requires more information."}
	}
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
			if f.Anonymous {
				embedded := wireSchema(f.Type)
				for name, schema := range embedded["properties"].(map[string]any) {
					props[name] = schema
				}
				required = append(required, embedded["required"].([]string)...)
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
