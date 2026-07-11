package query

import (
	"encoding/json"
	"math"
	"math/big"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/chcol"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func ptr[T any](v T) *T { return &v }

func TestIsDateOnly(t *testing.T) {
	// Exercises the unwrap loop directly: it must iterate through nested wrappers
	// to a leaf, return true only for a Date/Date32 leaf, and always terminate.
	tests := []struct {
		in   string
		want bool
	}{
		{"Date", true},
		{"Date32", true},
		{"DateTime", false},
		{"DateTime64(3)", false},
		{"Nullable(Date)", true},
		{"LowCardinality(Nullable(Date))", true}, // two hops
		{"SimpleAggregateFunction(anyLast, Date)", true},
		{"SimpleAggregateFunction(sum, UInt64)", false}, // wrapper, non-date leaf
		{"SimpleAggregateFunction(anyLast, DateTime)", false},
		{"Array(Date)", true},
		{"Array(Array(Date))", true}, // deep, must terminate
		{"SomeFutureWrapper(Date)", true},
		{"", false},
		{"Map(String, Date)", false}, // multi-arg, not a wrapper
	}
	for _, tt := range tests {
		if got := isDateOnly(tt.in); got != tt.want {
			t.Errorf("isDateOnly(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestFieldType(t *testing.T) {
	// The "(" guard distinguishes a named field ("d Date") from an unnamed field
	// whose type contains a space inside parens ("Decimal(10, 2)").
	tests := []struct{ in, want string }{
		{"d Date", "Date"},                                       // named leaf
		{"x Decimal(10, 2)", "Decimal(10, 2)"},                   // named, type keeps its inner space
		{"Decimal(10, 2)", "Decimal(10, 2)"},                     // unnamed: space is inside parens, keep whole
		{"Enum8('a' = 1)", "Enum8('a' = 1)"},                     // unnamed: spaces inside parens
		{"e Enum8('a' = 1, 'b' = 2)", "Enum8('a' = 1, 'b' = 2)"}, // named enum
		{"UInt8", "UInt8"},                                       // unnamed leaf
	}
	for _, tt := range tests {
		if got := fieldType(tt.in); got != tt.want {
			t.Errorf("fieldType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMapKeyString(t *testing.T) {
	date := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	tm := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		k       any
		keyType string
		want    string
	}{
		{date, "Date", "2026-07-12"},             // Date key renders calendar date
		{tm, "DateTime", "2026-07-12T09:00:00Z"}, // DateTime key keeps time
		{"plain", "String", "plain"},             // string key via fmt fallback
		{uint64(42), "UInt64", "42"},             // numeric key via fmt fallback
	}
	for _, tt := range tests {
		if got := mapKeyString(tt.k, tt.keyType); got != tt.want {
			t.Errorf("mapKeyString(%v, %q) = %q, want %q", tt.k, tt.keyType, got, tt.want)
		}
	}
}

// Inf/NaN must render as null so the WHOLE result still marshals — a raw
// non-finite float makes json.Marshal error and fails the entire response.
func TestInfNaN_ResultStillMarshals(t *testing.T) {
	// The raw input is what would break marshaling.
	if _, err := json.Marshal(math.Inf(1)); err == nil {
		t.Fatal("precondition: json.Marshal(+Inf) should error")
	}
	// The rendered value marshals cleanly to null.
	for _, in := range []float64{math.Inf(1), math.Inf(-1), math.NaN()} {
		b, err := json.Marshal(ToJSONValue(in, "Float64"))
		if err != nil || string(b) != "null" {
			t.Errorf("marshal(ToJSONValue(%v)) = %q, err=%v; want \"null\", nil", in, b, err)
		}
	}
	// A whole result row containing an Inf marshals rather than failing.
	row := []any{ToJSONValue(1.5, "Float64"), ToJSONValue(math.Inf(1), "Float64")}
	if b, err := json.Marshal(row); err != nil || string(b) != "[1.5,null]" {
		t.Errorf("marshal(row with Inf) = %q, err=%v; want [1.5,null]", b, err)
	}
}

func TestSplitTopLevel(t *testing.T) {
	tests := []struct {
		args string
		want []string
	}{
		{"String, Date", []string{"String", "Date"}},
		{"String, Map(String, Date)", []string{"String", "Map(String, Date)"}}, // comma inside Map is not a separator
		{"Date", []string{"Date"}},
		{"n UInt8, d Date", []string{"n UInt8", "d Date"}}, // named-tuple fields
		{"Tuple(Date, DateTime), UInt8", []string{"Tuple(Date, DateTime)", "UInt8"}},
		{"", nil},
	}
	for _, tt := range tests {
		if got := splitTopLevel(tt.args); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitTopLevel(%q) = %#v, want %#v", tt.args, got, tt.want)
		}
	}
}

func TestTypeNameAndElemType(t *testing.T) {
	nameCases := []struct{ in, name, args string }{
		{"Array(Date)", "Array", "Date"},
		{"Map(String, Date)", "Map", "String, Date"},
		{"Date", "Date", ""},
		{"Tuple(n UInt8, d Date)", "Tuple", "n UInt8, d Date"},
	}
	for _, c := range nameCases {
		if n, a := typeName(c.in); n != c.name || a != c.args {
			t.Errorf("typeName(%q) = (%q,%q), want (%q,%q)", c.in, n, a, c.name, c.args)
		}
	}
	elemCases := []struct{ in, want string }{
		{"Array(Date)", "Date"},
		{"Nullable(Date)", "Date"},
		{"LowCardinality(Date)", "Date"},
		{"Array(Array(Date))", "Array(Date)"},              // peels exactly one layer
		{"Map(String, Date)", "Map(String, Date)"},         // multi-arg, unchanged
		{"Tuple(Date, UInt8)", "Tuple(Date, UInt8)"},       // multi-arg, unchanged
		{"SimpleAggregateFunction(anyLast, Date)", "Date"}, // element is the LAST arg
		{"SimpleAggregateFunction(sum, UInt64)", "UInt64"}, //
		{"SomeFutureWrapper(Date)", "Date"},                // open-by-default single-arg unwrap
		{"Date", "Date"},
	}
	for _, c := range elemCases {
		if got := elemType(c.in); got != c.want {
			t.Errorf("elemType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

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
		{"date under a wrapper renders date-only", date, "SimpleAggregateFunction(anyLast, Date)", "2026-07-12"},
		{"datetime under a wrapper keeps time", tm, "SimpleAggregateFunction(anyLast, DateTime)", "2026-07-11T12:00:00Z"},
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
		// Inf/NaN have no JSON representation and would fail json.Marshal for the
		// whole result; render them as null (matching ClickHouse's JSON formats).
		{"finite float passthrough", float64(1.5), "Float64", float64(1.5)},
		{"float32 passthrough", float32(2.5), "Float32", float64(2.5)},
		{"+Inf -> null", math.Inf(1), "Float64", nil},
		{"-Inf -> null", math.Inf(-1), "Float64", nil},
		{"NaN -> null", math.NaN(), "Float64", nil},
		{"Inf inside array -> null", []float64{1.5, math.Inf(1)}, "Array(Float64)", []any{float64(1.5), nil}},
		// UUID/IP scan as byte types; render canonical strings, not byte arrays.
		{"uuid -> canonical string", uuid.MustParse("d592e5b1-7b76-42b0-8663-2b3197fbfc40"), "UUID", "d592e5b1-7b76-42b0-8663-2b3197fbfc40"},
		{"ipv4 -> dotted string", net.ParseIP("1.2.3.4"), "IPv4", "1.2.3.4"},
		{"ipv6 -> colon string", net.ParseIP("2001:db8::1"), "IPv6", "2001:db8::1"},
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
