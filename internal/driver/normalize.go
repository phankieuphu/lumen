package driver

import (
	sqldriver "database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"time"
	"unicode/utf8"
)

// Normalize converts an engine value into one that encodes to JSON without
// loss or surprise:
//
//   - nil, bool, string stay as they are
//   - integers that fit in a float64 exactly stay numeric; larger ones become
//     strings so JavaScript does not round them
//   - NaN and ±Inf become strings
//   - []byte becomes a string when it is valid UTF-8, else "\x" + base64
//   - time.Time becomes RFC 3339 with nanoseconds
//   - maps and slices of any (JSON columns) become raw JSON
//   - database/sql Valuers are unwrapped and normalized
//   - anything else uses fmt.Stringer, json.Marshaler, or fmt.Sprint
func Normalize(v any) any {
	switch x := v.(type) {
	case nil, bool, string:
		return x
	case int:
		return normInt(int64(x))
	case int8:
		return x
	case int16:
		return x
	case int32:
		return x
	case int64:
		return normInt(x)
	case uint8:
		return x
	case uint16:
		return x
	case uint32:
		return x
	case uint:
		return normUint(uint64(x))
	case uint64:
		return normUint(x)
	case float32:
		return normFloat(float64(x))
	case float64:
		return normFloat(x)
	case []byte:
		if utf8.Valid(x) {
			return string(x)
		}
		return `\x` + base64.StdEncoding.EncodeToString(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case *big.Int:
		return x.String()
	case map[string]any, []any:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return json.RawMessage(b)
	case sqldriver.Valuer:
		inner, err := x.Value()
		if err != nil {
			return fmt.Sprint(x)
		}
		if _, again := inner.(sqldriver.Valuer); again {
			return fmt.Sprint(inner)
		}
		return Normalize(inner)
	case fmt.Stringer:
		return x.String()
	case json.Marshaler:
		b, err := x.MarshalJSON()
		if err != nil {
			return fmt.Sprint(x)
		}
		return json.RawMessage(b)
	default:
		return fmt.Sprint(x)
	}
}

// maxSafeInt is the largest integer a float64 (JavaScript number) holds exactly.
const maxSafeInt = 1<<53 - 1

func normInt(n int64) any {
	if n > maxSafeInt || n < -maxSafeInt {
		return strconv.FormatInt(n, 10)
	}
	return n
}

func normUint(n uint64) any {
	if n > maxSafeInt {
		return strconv.FormatUint(n, 10)
	}
	return n
}

func normFloat(f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return f
}
