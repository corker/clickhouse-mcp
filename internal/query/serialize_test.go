package query

import (
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

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
