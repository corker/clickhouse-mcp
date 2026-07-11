package query

import (
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/chcol"
	"github.com/shopspring/decimal"
)

func ptr[T any](v T) *T { return &v }

// jsonValue builds a chcol.JSON value with one nested path set, as a scanned
// JSON column would arrive.
func jsonValue(path string, v any) chcol.JSON {
	j := chcol.NewJSON()
	j.SetValueAtPath(path, v)
	return *j
}

func TestToJSONValue(t *testing.T) {
	s := "hi"
	tm := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

	date := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		in     any
		dbType string
		want   any
	}{
		{"nil", nil, "", nil},
		{"uint64 max", uint64(18446744073709551615), "UInt64", "18446744073709551615"},
		{"uint64 small", uint64(42), "UInt64", "42"},
		{"int128 via big.Int", big.NewInt(0).SetBytes([]byte{1, 0, 0}), "Int128", "65536"},
		{"nil *big.Int", (*big.Int)(nil), "Int128", nil},
		{"decimal", decimal.RequireFromString("12345.678"), "Decimal(10, 3)", "12345.678"},
		{"datetime keeps RFC3339", tm, "DateTime", "2026-07-11T12:00:00Z"},
		{"date renders date-only", date, "Date", "2026-07-12"},
		{"date32 renders date-only", date, "Date32", "2026-07-12"},
		{"array of date renders date-only", []time.Time{date}, "Array(Date)", []any{"2026-07-12"}},
		{"nullable date renders date-only", &date, "Nullable(Date)", "2026-07-12"},
		{"datetime at midnight still keeps time", date, "DateTime", "2026-07-12T00:00:00Z"},
		{"nil *string", (*string)(nil), "", nil},
		{"non-nil *string", &s, "String", "hi"},
		{"array of uint8 -> numbers not base64", []byte{1, 2, 3}, "Array(UInt8)", []any{1, 2, 3}},
		{"empty byte array", []byte{}, "Array(UInt8)", []any{}},
		{"array of uint64 -> strings (recursive)", []uint64{1, 18446744073709551615}, "Array(UInt64)", []any{"1", "18446744073709551615"}},
		{"array of string passthrough", []string{"a", "b"}, "Array(String)", []any{"a", "b"}},
		{"array of decimal -> strings", []decimal.Decimal{decimal.RequireFromString("1.5")}, "Array(Decimal(10, 1))", []any{"1.5"}},
		{"nested array of uint64", [][]uint64{{1}, {2}}, "Array(Array(UInt64))", []any{[]any{"1"}, []any{"2"}}},
		{"map string->uint64 recurses", map[string]uint64{"k": 42}, "Map(String, UInt64)", map[string]any{"k": "42"}},
		{"typed nil pointer -> null", (*int32)(nil), "", nil},
		{"non-nil *int32 -> value", ptr(int32(5)), "Int32", int32(5)},
		{"non-nil *uint64 -> string", ptr(uint64(9)), "UInt64", "9"},
		{"plain int64 passthrough", int64(7), "Int64", int64(7)},
		{"plain string passthrough", "x", "String", "x"},
		{"plain bool passthrough", true, "Bool", true},
		// Variant/Dynamic/JSON: the LSP-fix branches — a big int inside a wrapper
		// type must still become a string, not a lossy JSON number.
		{"variant uint64 -> string", chcol.NewVariantWithType(uint64(18446744073709551615), "UInt64"), "Variant(UInt64)", "18446744073709551615"},
		{"variant nil -> null", chcol.NewVariant(nil), "Variant()", nil},
		{"variant string passthrough", chcol.NewVariant("hi"), "Variant(String)", "hi"},
		{"dynamic (alias) uint64 -> string", chcol.NewDynamicWithType(uint64(5), "UInt64"), "Dynamic", "5"},
		{"json nested uint64 -> string", jsonValue("a.b", uint64(9)), "JSON", map[string]any{"a": map[string]any{"b": "9"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToJSONValue(tt.in, tt.dbType)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ToJSONValue(%#v, %q) = %#v, want %#v", tt.in, tt.dbType, got, tt.want)
			}
		})
	}
}
