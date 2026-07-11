// Package query holds the pure logic for shaping ClickHouse results into
// JSON-safe structured output: value serialization and result bounding.
package query

import (
	"fmt"
	"math/big"
	"reflect"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/chcol"
	"github.com/shopspring/decimal"
)

// ToJSONValue converts a value scanned from the ClickHouse driver into a
// JSON-safe representation.
//
// Large and exact numerics are rendered as strings because a JSON number cannot
// hold them losslessly: UInt64/Int128/256 exceed 2^53 and Decimal must keep its
// scale. The contract applies recursively (via reflectValue), so the elements of
// Array(UInt64), Nullable, Map, and Variant/JSON get it too.
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
	case chcol.Variant:
		// Variant/Dynamic (Dynamic is an alias of Variant) wrap an opaque value;
		// unwrap and recurse so a big-int inside still becomes a string.
		if x.Nil() {
			return nil
		}
		return ToJSONValue(x.Any())
	case chcol.JSON:
		return ToJSONValue(x.NestedMap())
	default:
		return reflectValue(reflect.ValueOf(v))
	}
}

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
