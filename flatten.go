package autotel

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// flatten turns nested maps into dot-notation keys, the shape span attributes
// take: {"account": {"id": "acc_1"}} becomes {"account.id": "acc_1"}.
//
// Leaves the attribute setter understands (strings, numbers, bools and slices
// of them) pass through. A time becomes RFC 3339, an error its message, a
// fmt.Stringer its String(), and anything else the JSON it marshals to. Nil
// values are skipped, so an optional field left unset records nothing.
func flatten(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields))
	flattenInto(out, "", fields)

	return out
}

func flattenInto(out map[string]any, prefix string, fields map[string]any) {
	for key, value := range fields {
		if prefix != "" {
			key = prefix + "." + key
		}

		switch v := value.(type) {
		case nil:
		case map[string]any:
			flattenInto(out, key, v)
		case string, bool, int, int64, float64, []string, []int, []int64, []float64, []bool:
			out[key] = v
		case int8, int16, int32:
			out[key] = reflect.ValueOf(v).Int()
		case uint8, uint16, uint32:
			out[key] = int64(reflect.ValueOf(v).Uint())
		case uint, uint64:
			// Can exceed int64, so it is recorded exactly, as text.
			out[key] = fmt.Sprint(v)
		case float32:
			out[key] = float64(v)
		case time.Time:
			out[key] = v.Format(time.RFC3339Nano)
		case time.Duration:
			out[key] = v.String()
		case error:
			out[key] = v.Error()
		case fmt.Stringer:
			out[key] = v.String()
		default:
			if base, ok := baseValue(v); ok {
				out[key] = base
				continue
			}
			raw, err := json.Marshal(v)
			if err != nil {
				out[key] = "<serialization-failed>"
				continue
			}
			out[key] = string(raw)
		}
	}
}

// attributesFrom converts flattened fields to attributes, redacted the same
// way span attributes are.
func attributesFrom(fields map[string]any) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(fields))
	for key, value := range fields {
		attrs = append(attrs, redactedAttribute(key, value))
	}

	return attrs
}

// baseValue unwraps a named scalar (type Plan string, type Tier int) to its
// underlying value, so it records the way the plain type would.
func baseValue(v any) (any, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), true
	case reflect.Bool:
		return rv.Bool(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return int64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	default:
		return nil, false
	}
}
