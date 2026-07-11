// Package query holds the pure logic for shaping ClickHouse results into
// JSON-safe structured output: value serialization and result bounding.
package query

import (
	"math/big"
	"time"

	"github.com/shopspring/decimal"
)

// ToJSONValue converts a value scanned from the ClickHouse driver into a
// JSON-safe representation.
//
// Large and exact numeric types are rendered as strings because a JSON number
// cannot hold them without precision loss: UInt64 and Int128/256 exceed 2^53,
// and Decimal must keep its exact scale. Times become RFC3339 strings. A
// []byte scanned from Array(U?Int8) is rendered as a JSON array of numbers —
// the default JSON encoder would base64-encode it, which is wrong for a numeric
// array. Nil pointers become JSON null.
func ToJSONValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case uint64:
		return u64String(x)
	case *uint64:
		if x == nil {
			return nil
		}
		return u64String(*x)
	case *big.Int:
		if x == nil {
			return nil
		}
		return x.String()
	case decimal.Decimal:
		// Exact decimal — serialize via String() to preserve scale.
		return x.String()
	case *decimal.Decimal:
		if x == nil {
			return nil
		}
		return x.String()
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case *time.Time:
		if x == nil {
			return nil
		}
		return x.Format(time.RFC3339Nano)
	case []byte:
		// Array(U?Int8) scans to []byte; render as a numeric JSON array, not base64.
		out := make([]any, len(x))
		for i, b := range x {
			out[i] = int(b)
		}
		return out
	default:
		return derefPointer(x)
	}
}

func u64String(v uint64) string {
	return new(big.Int).SetUint64(v).String()
}

// derefPointer unwraps a non-nil pointer to a scalar (e.g. *string from
// Nullable columns) so the underlying value is marshaled; a nil pointer of any
// type becomes JSON null. Non-pointer values pass through unchanged.
func derefPointer(v any) any {
	switch x := v.(type) {
	case *string:
		if x == nil {
			return nil
		}
		return *x
	case *int64:
		if x == nil {
			return nil
		}
		return *x
	case *float64:
		if x == nil {
			return nil
		}
		return *x
	case *bool:
		if x == nil {
			return nil
		}
		return *x
	default:
		return v
	}
}
