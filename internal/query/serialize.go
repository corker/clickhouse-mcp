// Package query holds the pure logic for shaping ClickHouse results into
// JSON-safe structured output: value serialization and result bounding.
package query

import (
	"fmt"
	"math/big"
	"reflect"
	"time"

	"github.com/shopspring/decimal"
)

// ToJSONValue converts a value scanned from the ClickHouse driver into a
// JSON-safe representation.
//
// Large and exact numeric types are rendered as strings because a JSON number
// cannot hold them without precision loss: UInt64 and Int128/256 exceed 2^53,
// and Decimal must keep its exact scale. Times become RFC3339 strings.
//
// The driver's scan types are an open universe (Nullable(T) is a pointer, arrays
// are typed slices, Map/Tuple are maps/slices), so after the known scalar cases
// the function recurses reflectively: pointers are dereferenced (nil -> JSON
// null), and slices/arrays and maps are converted element-wise. This applies the
// numeric-string contract to array/map elements too — e.g. Array(UInt64) becomes
// an array of strings, not lossy JSON numbers. A []byte from Array(U?Int8) stays
// a numeric array (the default JSON encoder would base64-encode it).
func ToJSONValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case uint64:
		return u64String(x)
	case *big.Int:
		if x == nil {
			return nil
		}
		return x.String()
	case decimal.Decimal:
		return x.String()
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case []byte:
		// Array(U?Int8) scans to []byte; render as a numeric JSON array, not base64.
		out := make([]any, len(x))
		for i, b := range x {
			out[i] = int(b)
		}
		return out
	default:
		return reflectValue(reflect.ValueOf(v))
	}
}

// reflectValue applies the JSON contract to types not matched by the scalar
// cases above: pointers (Nullable columns), slices/arrays (Array columns), and
// maps (Map columns). Each element is routed back through ToJSONValue so the
// numeric-string and time rules apply recursively.
func reflectValue(rv reflect.Value) any {
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		return ToJSONValue(rv.Elem().Interface())
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = ToJSONValue(rv.Index(i).Interface())
		}
		return out
	case reflect.Map:
		// JSON object keys must be strings; ClickHouse Map keys are rendered via
		// fmt (they are typically String already).
		out := make(map[string]any, rv.Len())
		for _, k := range rv.MapKeys() {
			out[fmt.Sprintf("%v", k.Interface())] = ToJSONValue(rv.MapIndex(k).Interface())
		}
		return out
	default:
		// Scalars JSON encodes losslessly (int*, uint8/16/32, float*, string, bool).
		return rv.Interface()
	}
}

func u64String(v uint64) string {
	return new(big.Int).SetUint64(v).String()
}
