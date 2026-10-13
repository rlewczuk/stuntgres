package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func jsonCreateUnit(t *testing.T, kind JSONCreateKind, result *Descriptor, values []Value, variadic bool, env OutputEnvironment) Value {
	t.Helper()
	usages := make([]Usage, len(values))
	for i, v := range values {
		usages[i] = v.typ.Usage()
	}
	plan, err := BindJSONCreate(kind, result.Usage(), usages...)
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.Apply(context.Background(), values, variadic, env)
	if err != nil {
		t.Fatal(err)
	}
	if got.typ != result {
		t.Fatal("foreign result generation")
	}
	return got
}
func TestJSONCreateTypedPayload(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	one, _ := Integer(types[Int4], 1)
	array, err := ArrayValue(types[1007], []ArrayDimension{{Lower: -2, Length: 2}, {Lower: 5, Length: 2}}, []Value{one, Null(types[Int4]), one, one})
	if err != nil {
		t.Fatal(err)
	}
	rowType, err := RecordShape([]CompositeMember{{Name: "f1", Attribute: 1, Usage: types[1007].Usage()}, {Name: "f2", Attribute: 2, Usage: types[Text].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	row, err := OwnedCompositeValue(rowType, []Value{array, String(types[Text], "é\"\\\n")})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind  JSONCreateKind
		input Value
		want  string
	}{
		{ToJSON, row, `{"f1":[[1,null],[1,1]],"f2":"é\"\\\n"}`},
		{ToJSONB, row, `{"f1": [[1, null], [1, 1]], "f2": "é\"\\\n"}`},
		{ArrayToJSON, array, `[[1,null],[1,1]]`},
		{RowToJSON, row, `{"f1":[[1,null],[1,1]],"f2":"é\"\\\n"}`},
	} {
		result := types[JSON]
		if tc.kind.binary() {
			result = types[JSONB]
		}
		got := jsonCreateUnit(t, tc.kind, result, []Value{tc.input}, false, OutputEnvironment{})
		text, err := result.Output(ctx, got)
		if err != nil || text != tc.want {
			t.Fatalf("%s=%q err=%v", tc.kind.Name(), text, err)
		}
	}
	for _, kind := range []JSONCreateKind{JSONBuildArray, JSONBBuildArray} {
		result, want := types[JSON], `[1, null, 1, 1]`
		if kind.binary() {
			result = types[JSONB]
		}
		got := jsonCreateUnit(t, kind, result, []Value{array}, true, OutputEnvironment{})
		if got.text != want {
			t.Fatal(got.text)
		}
	}
	pretty := jsonCreateUnit(t, ArrayToJSON, types[JSON], []Value{array, Boolean(types[Bool], true)}, false, OutputEnvironment{})
	if pretty.text != "[[1,null],\n [1,1]]" {
		t.Fatal(pretty.text)
	}
	domain, err := Domain(90091, "jc_domain", "jc_domain", rowType.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(row)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonCreateUnit(t, ToJSON, types[JSON], []Value{wrapped}, false, OutputEnvironment{}); got.text != `{"f1":[[1,null],[1,1]],"f2":"é\"\\\n"}` {
		t.Fatal(got.text)
	}
	if array.ArrayDimension(0).Lower != -2 || row.CompositeField(0).array != array.array || wrapped.composite != row.composite {
		t.Fatal("converted input mutated")
	}
}

func TestJSONCreateStructuralSharingAndErrors(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	ctx := context.Background()
	embedded, err := types[JSONB].Input(ctx, `{"a":[1,2]}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	got := jsonCreateUnit(t, JSONBBuildArray, types[JSONB], []Value{embedded}, false, OutputEnvironment{})
	if &got.jsonb.root.items[0].fields[0] != &embedded.jsonb.root.fields[0] {
		t.Fatal("embedded immutable subtree cloned")
	}
	numeric, err := types[Numeric].Input(ctx, "12.340", -1)
	if err != nil {
		t.Fatal(err)
	}
	got = jsonCreateUnit(t, ToJSONB, types[JSONB], []Value{numeric}, false, OutputEnvironment{})
	if got.jsonb.root.number.scale != 3 || got.jsonb.root.number.digits != numeric.text || got.text != "12.340" {
		t.Fatal("numeric structure lost", got)
	}
	for _, kind := range []JSONCreateKind{JSONBuildObject, JSONBBuildObject} {
		result, want := types[JSON], `{"a" : 12.340, "a" : null}`
		if kind.binary() {
			result, want = types[JSONB], `{"a": null}`
		}
		values := []Value{String(types[Text], "a"), numeric, String(types[Text], "a"), Null(types[Int4])}
		if got := jsonCreateUnit(t, kind, result, values, false, OutputEnvironment{}); got.text != want {
			t.Fatal(got.text)
		}
	}
	invalid, err := types[JSON].Input(ctx, `"\u0000"`, -1)
	if err != nil {
		t.Fatal(err)
	}
	key := String(types[Text], "a")
	plan, err := BindJSONCreate(JSONBBuildObject, types[JSONB].Usage(), key.typ.Usage(), invalid.typ.Usage(), key.typ.Usage(), numeric.typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	failed, err := plan.Apply(ctx, []Value{key, invalid, key, numeric}, false, OutputEnvironment{})
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22P05" || failed != (Value{}) {
		t.Fatal("discarded invalid value not converted", err)
	}
	plan, err = BindJSONCreate(ToJSON, types[JSON].Usage(), types[Int4].Usage())
	if err != nil {
		t.Fatal(err)
	}
	foreign, _ := Integer(fresh[Int4], 1)
	if _, err := plan.Apply(ctx, []Value{foreign}, false, OutputEnvironment{}); err == nil {
		t.Fatal("foreign generation accepted")
	}
	if _, err := plan.Apply(ctx, []Value{{}}, false, OutputEnvironment{}); err == nil {
		t.Fatal("zero Value accepted")
	}
	if _, err := BindJSONCreate(ToJSON, types[JSONB].Usage(), types[Int4].Usage()); err == nil {
		t.Fatal("incorrect result category accepted")
	}
}

type jsonCreateWorkContext struct {
	context.Context
	remaining int
}

func (c *jsonCreateWorkContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
func TestJSONCreateCancellationAndDeepTraversal(t *testing.T) {
	types := testTypes()
	value := String(types[Text], "leaf")
	for range 80 {
		shape, err := RecordShape([]CompositeMember{{Name: "f1", Attribute: 1, Usage: value.typ.Usage()}})
		if err != nil {
			t.Fatal(err)
		}
		value, err = OwnedCompositeValue(shape, []Value{value})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []JSONCreateKind{ToJSON, ToJSONB} {
		result := types[JSON]
		if kind.binary() {
			result = types[JSONB]
		}
		got := jsonCreateUnit(t, kind, result, []Value{value}, false, OutputEnvironment{})
		want := strings.Repeat(`{"f1":`, 80) + `"leaf"` + strings.Repeat("}", 80)
		if kind.binary() {
			want = strings.Repeat(`{"f1": `, 80) + `"leaf"` + strings.Repeat("}", 80)
		}
		if got.text != want {
			t.Fatal("deep traversal changed shape")
		}
	}
	long := String(types[Text], strings.Repeat("é\"\\", 10000))
	for _, tc := range []struct {
		kind   JSONCreateKind
		values []Value
	}{
		{ToJSON, []Value{long}}, {JSONBuildObject, []Value{long, long}}, {ToJSONB, []Value{value}},
		{JSONBBuildObject, []Value{long, long, long, long}},
	} {
		result := types[JSON]
		if tc.kind.binary() {
			result = types[JSONB]
		}
		usages := make([]Usage, len(tc.values))
		for i, v := range tc.values {
			usages[i] = v.typ.Usage()
		}
		plan, err := BindJSONCreate(tc.kind, result.Usage(), usages...)
		if err != nil {
			t.Fatal(err)
		}
		ctx := &jsonCreateWorkContext{Context: context.Background(), remaining: 8}
		got, err := plan.Apply(ctx, tc.values, false, OutputEnvironment{})
		if !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal("cancellation not atomic", err)
		}
	}
}

func TestJSONCreateAnonymousRecordInputBoundary(t *testing.T) {
	shape, err := RecordShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !shape.Concrete() {
		t.Fatal("anonymous transport is not concrete")
	}
	for _, text := range []string{"x", "()", ""} {
		_, err := shape.Input(context.Background(), text, -1)
		var d *sqlstate.Error
		if !errors.As(err, &d) || *d != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "input of anonymous composite types is not implemented"}) {
			t.Fatal(err)
		}
	}
	types := testTypes()
	got := jsonCreateUnit(t, RowToJSON, types[JSON], []Value{Null(shape)}, false, OutputEnvironment{})
	if !got.null || got.typ != types[JSON] {
		t.Fatal("anonymous typed NULL lost")
	}
}

func TestJSONCreatePrivateCarriersAndTemporal(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		value Value
		want  string
	}{
		{Value{typ: types[Xid], oid: 12}, `"12"`},
		{String(types[Char], "\x80"), `"\\200"`},
		{Value{typ: types[PgNodeTree], treeKey: true, text: "{CONST :value 1}"}, `"{CONST :value 1}"`},
		{VoidValue(types[Void]), `""`},
		{Value{typ: types[Date], integer: 0}, `"2000-01-01"`},
		{Value{typ: types[Timestamp], integer: 123400}, `"2000-01-01T00:00:00.1234"`},
		{Value{typ: types[Timestamptz], integer: 0}, `"2000-01-01T00:00:00+00:00"`},
		{Value{typ: types[Date], integer: DateNegativeInfinity}, `"-infinity"`},
		{Value{typ: types[Timestamptz], integer: TimestampPositiveInfinity}, `"infinity"`},
	} {
		for _, kind := range []JSONCreateKind{ToJSON, ToJSONB} {
			result := types[JSON]
			if kind.binary() {
				result = types[JSONB]
			}
			got := jsonCreateUnit(t, kind, result, []Value{tc.value}, false, OutputEnvironment{})
			if got.text != tc.want {
				t.Fatalf("%s %s=%q want %q", kind.Name(), tc.value.typ.Name(), got.text, tc.want)
			}
		}
	}
}

func TestJSONCreateNormalizationCancellation(t *testing.T) {
	types := testTypes()
	prefix := strings.Repeat("same-prefix", 10000)
	key := String(types[Text], prefix+"a")
	other := String(types[Text], prefix+"b")
	one, _ := Integer(types[Int4], 1)
	plan, err := BindJSONCreate(JSONBBuildObject, types[JSONB].Usage(), key.typ.Usage(), one.typ.Usage(), other.typ.Usage(), one.typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	ctx := &jsonCreateWorkContext{Context: context.Background(), remaining: 100}
	got, err := plan.Apply(ctx, []Value{key, one, other, one}, false, OutputEnvironment{})
	if !errors.Is(err, context.Canceled) || got != (Value{}) {
		t.Fatal("long-key normalization ignored cancellation", err)
	}
	if key.text != prefix+"a" || other.text != prefix+"b" {
		t.Fatal("key inputs mutated")
	}
}
