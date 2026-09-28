// Package values converts driver and Redis values into JSON-safe values.
package values

import (
	"encoding/base64"
	"fmt"
	"time"
	"unicode/utf8"
)

// JSON converts a driver or Redis value into a JSON-safe value:
// nil, bool, int64, float64, string, or a composite of those.
func JSON(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case bool, int64, float64, string:
		return t
	case int:
		return int64(t)
	case int8:
		return int64(t)
	case int16:
		return int64(t)
	case int32:
		return int64(t)
	case uint8:
		return int64(t)
	case uint16:
		return int64(t)
	case uint32:
		return int64(t)
	case uint64:
		return int64(t)
	case float32:
		return float64(t)
	case []byte:
		if utf8.Valid(t) {
			return string(t)
		}
		return "b64:" + base64.StdEncoding.EncodeToString(t)
	case time.Time:
		return t.Format(time.RFC3339)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = JSON(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = JSON(e)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[fmt.Sprintf("%v", k)] = JSON(e)
		}
		return out
	default:
		return fmt.Sprintf("%v", t)
	}
}
