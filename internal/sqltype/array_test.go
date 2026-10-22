package sqltype

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestArrayBuiltinAuthority(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	for _, tc := range []struct {
		id, element       ID
		name, declaration string
	}{
		{1000, Bool, "_bool", "BOOLEAN[]"}, {1001, Bytea, "_bytea", "BYTEA[]"}, {1003, Name, "_name", "NAME[]"},
		{1005, Int2, "_int2", "SMALLINT[]"}, {1007, Int4, "_int4", "INTEGER[]"}, {1009, Text, "_text", "TEXT[]"},
		{1011, Xid, "_xid", "XID[]"},
		{1014, BPChar, "_bpchar", "BPCHAR[]"}, {1015, VarChar, "_varchar", "VARCHAR[]"}, {1016, Int8, "_int8", "BIGINT[]"},
		{1021, Float4, "_float4", "REAL[]"}, {1022, Float8, "_float8", "DOUBLE PRECISION[]"}, {1028, Oid, "_oid", "OID[]"},
		{1115, Timestamp, "_timestamp", "TIMESTAMP[]"}, {1182, Date, "_date", "DATE[]"},
		{1183, Time, "_time", "TIME[]"}, {1185, Timestamptz, "_timestamptz", "TIMESTAMP WITH TIME ZONE[]"},
		{1187, Interval, "_interval", "INTERVAL[]"}, {1231, Numeric, "_numeric", "NUMERIC[]"}, {1270, Timetz, "_timetz", "TIME WITH TIME ZONE[]"},
		{2210, Regclass, "_regclass", "REGCLASS[]"}, {2951, UUID, "_uuid", "UUID[]"},
	} {
		d := types[tc.id]
		if d == nil || !d.IsArray() || d.IsDomain() || d.Element() != types[tc.element] || d.ID() != tc.id || d.Name() != tc.name || d.SQLName() != tc.declaration || d.DatabaseName() != strings.ToUpper(tc.name) || types[tc.element].AssociatedArray() != tc.id {
			t.Fatal("builtin array authority", tc, d)
		}
		if d == fresh[tc.id] || d.Element() == fresh[tc.element] || fresh[tc.id].Element() != fresh[tc.element] {
			t.Fatal("shared or foreign builtin generation", tc)
		}
	}
	// Catalog identity is not executable array authority.
	for _, id := range []ID{Unknown, Char, Void, PgNodeTree, Cid, Xid8} {
		if _, err := Array(90000, "_boundary", types[id]); err == nil {
			t.Fatal("unsupported scalar admitted to stored array", id)
		}
	}
	for _, id := range []ID{1002, 1012, 271} {
		if _, err := ArrayValue(types[id], nil, nil); err == nil {
			t.Fatal("lookup-only array admitted an empty value", id)
		}
		if _, err := types[id].Input(context.Background(), "{}", -1); err == nil {
			t.Fatal("lookup-only array admitted textual input", id)
		}
	}
}

func TestArrayPayloadOwnershipAndValidation(t *testing.T) {
	types := testTypes()
	d := types[1009]
	dims := []ArrayDimension{{Lower: -1, Length: 1}, {Lower: 3, Length: 2}}
	elements := []Value{String(types[Text], "a"), Null(types[Text])}
	value, err := ArrayValue(d, dims, elements)
	if err != nil {
		t.Fatal(err)
	}
	dims[0].Lower = 99
	elements[0] = String(types[Text], "changed")
	exposed := value.ArrayDimensions()
	exposed[0].Lower = 88
	if value.ArrayDimensions()[0].Lower != -1 || value.ArrayLen() != 2 || !value.ArrayElement(1).IsNull() {
		t.Fatal("mutable array ownership")
	}
	text, err := d.Output(context.Background(), value)
	if err != nil || text != "[-1:-1][3:4]={{a,NULL}}" {
		t.Fatal(text, err)
	}
	copied := value
	if copied.array != value.array || !SameDatum(copied, value) {
		t.Fatal("copy reconstructed payload")
	}
	independently, err := d.Input(context.Background(), text, -1)
	if err != nil || independently.array == value.array || !SameDatum(independently, value) {
		t.Fatal("structural identity", err)
	}
	empty, err := ArrayValue(d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if empty.IsNull() || empty.ArrayLen() != 0 || len(empty.ArrayDimensions()) != 0 || SameDatum(empty, Null(d)) {
		t.Fatal("empty/whole NULL identity")
	}
	for _, tc := range []struct {
		typ      *Descriptor
		dims     []ArrayDimension
		elements []Value
	}{
		{nil, nil, nil}, {types[Text], nil, nil}, {d, nil, []Value{String(types[Text], "a")}},
		{d, []ArrayDimension{{Lower: 1, Length: 0}}, nil}, {d, []ArrayDimension{{Lower: math.MaxInt32, Length: 1}}, []Value{String(types[Text], "a")}},
		{d, []ArrayDimension{{Lower: 1, Length: 2}}, []Value{String(types[Text], "a")}},
		{d, []ArrayDimension{{Lower: 1, Length: math.MaxInt32}, {Lower: 1, Length: 2}}, nil},
		{d, []ArrayDimension{{Lower: 1, Length: 1}}, []Value{Null(testTypes()[Text])}},
		{d, []ArrayDimension{{Lower: 1, Length: 1}}, []Value{String(testTypes()[Text], "a")}},
		{d, []ArrayDimension{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}, []Value{String(types[Text], "a")}},
	} {
		if _, err := ArrayValue(tc.typ, tc.dims, tc.elements); err == nil {
			t.Fatal("accepted invalid payload", tc)
		}
	}
	for _, element := range []*Descriptor{nil, types[Unknown], types[Void], types[Char], types[PgNodeTree], types[1007]} {
		if _, err := Array(60000, "_invalid", element); err == nil {
			t.Fatal("accepted excluded element", element)
		}
	}
}

func TestArrayCodecIndependentGoldens(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, tc := range []struct {
		id          ID
		input, want string
	}{
		{1007, "[-1:0][3:4]={{1,NULL},{2,3}}", "[-1:0][3:4]={{1,NULL},{2,3}}"},
		{1007, "[1:2][1:2]={{1,2},{3,4}}", "{{1,2},{3,4}}"}, {1007, "[2]={1,7}", "{1,7}"},
		{1007, "{{},{}}", "{}"}, {1007, "{{{1}}}", "{{{1}}}"}, {1007, "{{{{{{1}}}}}}", "{{{{{{1}}}}}}"},
		{1009, `{NULL,"NULL","",alpha}`, `{NULL,"NULL","",alpha}`},
		{1009, `{\NULL,N\ULL,NUL\L,\N\U\L\L}`, `{"NULL","NULL","NULL","NULL"}`},
		{1009, `{"a,b","a{b}","a b","a\"b","a\\b","O'Brien"}`, `{"a,b","a{b}","a b","a\"b","a\\b",O'Brien}`},
		{1009, `{ a b , a\ ,\ a}`, `{"a b","a "," a"}`},
		{1000, "{true,no,on,NULL}", "{t,f,t,NULL}"},
		{1003, "{" + strings.Repeat("é", 32) + "}", "{" + strings.Repeat("é", 31) + "}"},
		{1001, `{"\\x00ff",NULL}`, `{"\\x00ff",NULL}`},
	} {
		d := types[tc.id]
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(tc, err)
		}
		got, err := d.Output(ctx, v)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
		reparsed, err := d.Input(ctx, got, -1)
		if err != nil || !SameDatum(v, reparsed) {
			t.Fatal("canonical round trip", tc, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+strings.ReplaceAll(tc.want, "'", "''")+"'" {
			t.Fatal("outer SQL quoting", tc, literal, err)
		}
	}
}

func TestArrayDelegatedDelimiterAndNestedDomain(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	semicolon := *types[Text]
	semicolon.delimiter = ';'
	domain, err := Domain(60001, "semi", "public.semi", semicolon.Usage())
	if err != nil {
		t.Fatal(err)
	}
	d, err := Array(60002, "_semi", domain)
	if err != nil {
		t.Fatal(err)
	}
	v, err := d.Input(ctx, `{a,b;NULL;"a;b"}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.Output(ctx, v)
	if err != nil || got != `{a,b;NULL;"a;b"}` || d.Element().Delimiter() != ';' {
		t.Fatal(got, err)
	}
	whole, err := Domain(60003, "whole", "public.whole", types[1007].Usage())
	if err != nil {
		t.Fatal(err)
	}
	nested, err := Array(60004, "_whole", whole)
	if err != nil {
		t.Fatal(err)
	}
	value, err := nested.Input(ctx, `{"[-1:0]={1,NULL}",NULL,"{}"}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	text, err := nested.Output(ctx, value)
	if err != nil || text != `{"[-1:0]={1,NULL}",NULL,"{}"}` {
		t.Fatal(text, err)
	}
	fresh := testTypes()
	freshWhole, err := Domain(60003, "whole", "public.whole", fresh[1007].Usage())
	if err != nil {
		t.Fatal(err)
	}
	freshArray, err := Array(60004, "_whole", freshWhole)
	if err != nil {
		t.Fatal(err)
	}
	rebound, err := value.Retag(freshArray)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.Type() != freshArray || rebound.ArrayElement(0).Type() != freshWhole || rebound.ArrayElement(0).ArrayElement(0).Type() != fresh[Int4] || rebound.ArrayElement(1).Type() != freshWhole || value.ArrayElement(0).Type() != whole || rebound.array == value.array {
		t.Fatal("recursive generation reownership")
	}
	unchanged, err := rebound.Retag(freshArray)
	if err != nil || unchanged != rebound || unchanged.array != rebound.array || unchanged.ArrayElement(0).array != rebound.ArrayElement(0).array {
		t.Fatal("unchanged value or nested payload rebuilt", err)
	}
	if _, err := BindOrdering(nested.Usage()); err == nil {
		t.Fatal("array ordering admitted")
	}
	comparison, err := BindComparison(nested, nested, nested.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	if equal, err := comparison.EqualValues(ctx, value, value, InputEnvironment{}); err != nil || !equal {
		t.Fatal("nested array equality", equal, err)
	}
	if sign, err := comparison.Compare(ctx, value, value, InputEnvironment{}); err != nil || sign != 0 {
		t.Fatal("nested array ordering", sign, err)
	}
	if StringScalar(nested) || StringScalar(whole) {
		t.Fatal("array scalar string admission")
	}
}

func TestArrayDiagnosticsAndCancellation(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct{ input, detail string }{
		{"x", `Array value must start with "{" or dimension information.`},
		{"[1:2]{1,2}", `Missing "=" after array dimensions.`}, {"[1:2]=x", `Array contents must start with "{".`},
		{"[:1]={1}", `"[" must introduce explicitly-specified array dimensions.`}, {"[ 1:2]={1,2}", `"[" must introduce explicitly-specified array dimensions.`},
		{"[1:]={1}", "Missing array dimension value."}, {"[1 :2]={1,2}", `Missing "]" after array dimensions.`},
		{"[2]={1}", "Specified array dimensions do not match array contents."},
		{"{{1},{2,3}}", "Multidimensional arrays must have sub-arrays with matching dimensions."},
		{"{{1},{{2}}}", "Multidimensional arrays must have sub-arrays with matching dimensions."},
		{"{{},{1}}", "Multidimensional arrays must have sub-arrays with matching dimensions."},
		{"{1,,2}", `Unexpected "," character.`}, {"{1,}", `Unexpected "}" character.`},
		{"{foo{}}", `Unexpected "{" character.`}, {`{"foo"{}}`, `Unexpected "{" character.`},
		{`{"a"b}`, "Incorrectly quoted array element."}, {`{a"b"}`, "Incorrectly quoted array element."}, {`{"a""b"}`, "Incorrectly quoted array element."},
		{`{{"1 2"} x,{3}}`, "Unexpected array element."}, {`{"unterminated}`, "Unexpected end of input."}, {`{a\}`, "Unexpected end of input."},
		{"{}}", "Junk after closing right brace."}, {"{1}x", "Junk after closing right brace."},
	} {
		_, err := types[1009].Input(ctx, tc.input, -1)
		arrayUnitDiagnostic(t, err, "22P02", `malformed array literal: "`+tc.input+`"`, tc.detail)
	}
	for _, tc := range []struct{ input, code, message string }{
		{"[1:0]={}", "2202E", "upper bound cannot be less than lower bound"},
		{"[2147483646:2147483647]={1,2}", "54000", "array upper bound is too large: 2147483647"},
		{"[2147483648:2147483648]={1}", "54000", "array bound is out of integer range"},
		{"{{{{{{{1}}}}}}}", "54000", "number of array dimensions exceeds the maximum allowed (6)"},
		{"{1,oops}", "22P02", `invalid input syntax for type integer: "oops"`},
		{"{1,2147483648}", "22003", `value "2147483648" is out of range for type integer`},
		{"{{oops},{1,2}}", "22P02", `invalid input syntax for type integer: "oops"`},
		{"{oops,}", "22P02", `invalid input syntax for type integer: "oops"`},
	} {
		_, err := types[1007].Input(ctx, tc.input, -1)
		arrayUnitDiagnostic(t, err, tc.code, tc.message, "")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := types[1007].Input(canceled, "{1}", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	value, err := types[1007].Input(ctx, "{1}", -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := types[1007].Output(canceled, value); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func arrayUnitDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	var got *sqlstate.Error
	if !errors.As(err, &got) {
		t.Fatal("non-SQL input diagnostic", err)
	}
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("diagnostic=%#v want %#v", *got, want)
	}
}

func TestArrayElementModifiersAndMetadata(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		id                       ID
		mods                     []int32
		declaration, input, want string
	}{
		{1015, []int32{3}, "VARCHAR(3)[]", "[0:1]={abcd,NULL}", "[0:1]={abc,NULL}"},
		{1014, []int32{3}, "CHARACTER(3)[]", "{a,NULL}", `{"a  ",NULL}`},
		{1231, []int32{5, 2}, "NUMERIC(5,2)[]", "{12.345,NULL}", "{12.35,NULL}"},
		{1183, []int32{3}, "TIME(3)[]", "{12:00:00.123789,NULL}", "{12:00:00.124,NULL}"},
		{1115, []int32{3}, "TIMESTAMP(3)[]", `{"2024-02-29 12:00:00.123789",NULL}`, `{"2024-02-29 12:00:00.124",NULL}`},
	} {
		d := types[tc.id]
		u, _, err := d.ResolveModifiers(tc.mods...)
		if err != nil {
			t.Fatal(tc, err)
		}
		declaration, err := u.Declaration()
		if err != nil || declaration != tc.declaration {
			t.Fatal(tc, declaration, err)
		}
		value, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(tc, err)
		}
		plan, err := PlanCoercion(d.Usage(), u, Explicit)
		if err != nil {
			t.Fatal(tc, err)
		}
		sized, err := plan.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(tc, err)
		}
		got, err := d.Output(ctx, sized)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
		if _, known := u.Length(); known {
			t.Fatal("array exposed element length", tc)
		}
	}
	if p, s, known := types[1231].Usage().DecimalSize(); p != 0 || s != 0 || known {
		t.Fatal(p, s, known)
	}
	numeric, _, err := types[1231].ResolveModifiers(5, 2)
	if err != nil {
		t.Fatal(err)
	}
	if p, s, known := numeric.DecimalSize(); p != 5 || s != 2 || !known {
		t.Fatal(p, s, known)
	}
	for _, id := range []ID{1003, 1009, 1014, 1015} {
		if types[id].Usage().Collation != types[types[id].Element().ID()].Usage().Collation {
			t.Fatal("array collation", id)
		}
	}
}
