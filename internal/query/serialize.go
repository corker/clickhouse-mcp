// Package query holds the pure logic for shaping ClickHouse results into
// JSON-safe structured output: value serialization and result bounding.
package query

import (
	"fmt"
	"math"
	"math/big"
	"net"
	"reflect"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/chcol"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const dateLayout = "2006-01-02"

// ToJSONValue converts a value scanned from the ClickHouse driver into a
// JSON-safe representation. dbType is the column's ClickHouse type name (from
// ColumnType.DatabaseTypeName), used only to tell date-only types apart from
// datetimes — the driver scans Date, Date32, and DateTime all into time.Time, so
// the value alone cannot distinguish a calendar date from a midnight datetime.
//
// Large and exact numerics are rendered as strings because a JSON number cannot
// hold them losslessly: UInt64/Int128/256 exceed 2^53 and Decimal must keep its
// scale. The contract applies recursively (via reflectValue), so the elements of
// Array(UInt64), Nullable, Map, and Variant/JSON get it too.
func ToJSONValue(v any, dbType string) any {
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
	case float64:
		return finiteOrNull(x)
	case float32:
		return finiteOrNull(float64(x))
	case uuid.UUID:
		// Scans as [16]byte; render the canonical UUID string, not a byte array.
		return x.String()
	case net.IP:
		// IPv4/IPv6 scan as net.IP ([]byte); render the canonical address string.
		// Must precede the []byte case, which would otherwise emit raw octets.
		return x.String()
	case time.Time:
		// A Date/Date32 has no time-of-day; render it as a calendar date rather
		// than inventing a midnight-UTC datetime. DateTime/DateTime64 keep RFC3339.
		if isDateOnly(dbType) {
			return x.Format(dateLayout)
		}
		return x.Format(time.RFC3339Nano)
	case []byte:
		// Array(U?Int8) scans to []byte; render as a numeric JSON array, not base64.
		// NOTE: any new type that is a []byte alias (like net.IP above) must be cased
		// ABOVE this arm — a type switch matches top-down, so a case added below is
		// silently shadowed here.
		out := make([]any, len(x))
		for i, b := range x {
			out[i] = int(b)
		}
		return out
	case chcol.Variant:
		// Variant/Dynamic (Dynamic is an alias of Variant) wrap an opaque value;
		// unwrap and recurse so a big-int inside still becomes a string. The wrapper
		// carries no outer dbType for the inner value, so date-only info is lost here
		// (a Date inside a Variant renders as a datetime — an accepted edge).
		if x.Nil() {
			return nil
		}
		return ToJSONValue(x.Any(), "")
	case chcol.JSON:
		return ToJSONValue(x.NestedMap(), "")
	default:
		return reflectValue(reflect.ValueOf(v), dbType)
	}
}

// isDateOnly reports whether a ClickHouse type is a calendar date (no time
// component), unwrapping single-element wrappers so a value scanned directly as
// time.Time under, e.g., SimpleAggregateFunction(anyLast, Date) or Nullable(Date)
// is still recognized.
func isDateOnly(dbType string) bool {
	t := strings.TrimSpace(dbType)
	for {
		if t == "Date" || t == "Date32" {
			return true
		}
		if inner := elemType(t); inner != t {
			t = strings.TrimSpace(inner)
			continue
		}
		return false
	}
}

// typeName returns the outer type constructor of a ClickHouse type ("Array" for
// "Array(Date)", "Date" for "Date"), and its argument list verbatim ("Date", or
// "String, Date" for a Map). A leaf type has an empty arg string.
func typeName(dbType string) (name, args string) {
	dbType = strings.TrimSpace(dbType)
	open := strings.IndexByte(dbType, '(')
	if open < 0 || !strings.HasSuffix(dbType, ")") {
		return dbType, ""
	}
	return dbType[:open], dbType[open+1 : len(dbType)-1]
}

// splitTopLevel splits a ClickHouse type-argument list on commas that are not
// nested inside parens, so "String, Map(String, Date)" yields the two arguments
// rather than three. Element names on named-tuple fields are left attached.
func splitTopLevel(args string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(args[start:i]))
				start = i + 1
			}
		}
	}
	if s := strings.TrimSpace(args[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// elemType strips one wrapper to its element type: Array(T)/Nullable(T)/
// LowCardinality(T) carry a single element T, and SimpleAggregateFunction(fn, T)
// carries T as its last argument. Multi-argument types the caller handles
// positionally (Map, Tuple, Variant) and leaves are returned unchanged.
//
// Open-by-default on single-arg wrappers so a new CH wrapper unwraps rather than
// silently mis-threading its type name into recursion (which would, e.g., render
// a wrapped Date as a datetime).
func elemType(dbType string) string {
	name, args := typeName(dbType)
	switch name {
	case "", "Map", "Tuple", "Variant":
		return dbType
	case "SimpleAggregateFunction":
		// SimpleAggregateFunction(func, T): the value's type is the last argument.
		if parts := splitTopLevel(args); len(parts) >= 2 {
			return parts[len(parts)-1]
		}
		return dbType
	}
	if inner := splitTopLevel(args); len(inner) == 1 {
		return inner[0]
	}
	return dbType
}

// fieldType strips an optional "name " prefix from a named-tuple field so only
// the type remains ("d Date" -> "Date"). A leading quoted or bare identifier
// followed by a space and a type is treated as a name.
func fieldType(field string) string {
	if sp := strings.IndexByte(field, ' '); sp > 0 {
		// A named field is "<ident> <type>". The first space either follows a bare
		// field identifier (a real name) or sits inside a type's parens (e.g. the
		// space in "Decimal(10, 2)" or "Enum8('a' = 1)"). The "(" check distinguishes
		// them: if text before the first space contains "(", the space is inside a
		// type, so this is an unnamed field — return it whole.
		if rest := strings.TrimSpace(field[sp+1:]); rest != "" && !strings.ContainsAny(field[:sp], "(") {
			return rest
		}
	}
	return field
}

func reflectValue(rv reflect.Value, dbType string) any {
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		// Nullable(X): the value carries X's type.
		return ToJSONValue(rv.Elem().Interface(), elemType(dbType))
	case reflect.Slice, reflect.Array:
		// Array(X): every element is X. An unnamed Tuple also scans to a slice —
		// then each position has its own type.
		name, args := typeName(dbType)
		var elemTypes []string
		if name == "Tuple" {
			elemTypes = splitTopLevel(args)
		}
		inner := elemType(dbType)
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			t := inner
			if i < len(elemTypes) {
				t = fieldType(elemTypes[i])
			}
			out[i] = ToJSONValue(rv.Index(i).Interface(), t)
		}
		return out
	case reflect.Map:
		// Map(K, V) — the value carries V; the key carries K (and must render as a
		// string for a JSON object). A named Tuple also scans to a map keyed by
		// field name, so look each field's type up by name.
		name, args := typeName(dbType)
		keyType, valType := "", ""
		fieldTypes := map[string]string{}
		switch name {
		case "Map":
			if kv := splitTopLevel(args); len(kv) == 2 {
				keyType, valType = kv[0], kv[1]
			}
		case "Tuple":
			for _, f := range splitTopLevel(args) {
				if sp := strings.IndexByte(f, ' '); sp > 0 {
					fieldTypes[f[:sp]] = strings.TrimSpace(f[sp+1:])
				}
			}
		}
		out := make(map[string]any, rv.Len())
		for _, k := range rv.MapKeys() {
			key := mapKeyString(k.Interface(), keyType)
			vt := valType
			if ft, ok := fieldTypes[key]; ok {
				vt = ft
			}
			out[key] = ToJSONValue(rv.MapIndex(k).Interface(), vt)
		}
		return out
	default:
		// Small scalars JSON encodes losslessly (int*, uint8/16/32, string, bool).
		// Floats are handled in ToJSONValue (Inf/NaN → null) before reaching here.
		return rv.Interface()
	}
}

// mapKeyString renders a Map key as a JSON object key. A Date/Date32 key uses the
// calendar-date form (not Go's default time.Time string); everything else uses
// fmt, since ClickHouse Map keys are usually already strings or numbers.
func mapKeyString(k any, keyType string) string {
	if t, ok := k.(time.Time); ok {
		if isDateOnly(keyType) {
			return t.Format(dateLayout)
		}
		return t.Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%v", k)
}

func u64String(v uint64) string {
	return new(big.Int).SetUint64(v).String()
}

// finiteOrNull returns v as-is, or nil for Inf/NaN — JSON has no representation
// for them and json.Marshal errors on them, which would fail the whole result.
// ClickHouse's own JSON formats render these as null, so this matches.
func finiteOrNull(v float64) any {
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return nil
	}
	return v
}
