package query

import (
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func ptr[T any](v T) *T { return &v }

func TestToJSONValue(t *testing.T) {
	s := "hi"
	tm := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		in   any
		want any
	}{
		{"nil", nil, nil},
		{"uint64 max", uint64(18446744073709551615), "18446744073709551615"},
		{"uint64 small", uint64(42), "42"},
		{"int128 via big.Int", big.NewInt(0).SetBytes([]byte{1, 0, 0}), "65536"},
		{"nil *big.Int", (*big.Int)(nil), nil},
		{"decimal", decimal.RequireFromString("12345.678"), "12345.678"},
		{"time", tm, "2026-07-11T12:00:00Z"},
		{"nil *string", (*string)(nil), nil},
		{"non-nil *string", &s, "hi"},
		{"array of uint8 -> numbers not base64", []byte{1, 2, 3}, []any{1, 2, 3}},
		{"empty byte array", []byte{}, []any{}},
		{"array of uint64 -> strings (recursive)", []uint64{1, 18446744073709551615}, []any{"1", "18446744073709551615"}},
		{"array of string passthrough", []string{"a", "b"}, []any{"a", "b"}},
		{"array of decimal -> strings", []decimal.Decimal{decimal.RequireFromString("1.5")}, []any{"1.5"}},
		{"nested array of uint64", [][]uint64{{1}, {2}}, []any{[]any{"1"}, []any{"2"}}},
		{"map string->uint64 recurses", map[string]uint64{"k": 42}, map[string]any{"k": "42"}},
		{"typed nil pointer -> null", (*int32)(nil), nil},
		{"non-nil *int32 -> value", ptr(int32(5)), int32(5)},
		{"non-nil *uint64 -> string", ptr(uint64(9)), "9"},
		{"plain int64 passthrough", int64(7), int64(7)},
		{"plain string passthrough", "x", "x"},
		{"plain bool passthrough", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToJSONValue(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ToJSONValue(%#v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}
