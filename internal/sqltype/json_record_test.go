package sqltype

import (
	"context"
	"errors"
	"testing"
)

func jsonRecordUnitTypes(t *testing.T) (map[ID]*Descriptor, *Descriptor) {
	t.Helper()
	types := make(map[ID]*Descriptor)
	for _, d := range Builtins() {
		types[d.ID()] = d
	}
	shape, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "a", Usage: types[Int4].Usage()}, {Attribute: 2, Name: "b", Usage: types[Text].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	return types, shape
}
func jsonRecordUnitJSON(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestJSONRecordPlan(t *testing.T) {
	types, shape := jsonRecordUnitTypes(t)
	for _, name := range []string{"json_to_record", "json_to_recordset"} {
		p, err := BindJSONRecord(name, shape.Usage(), -1, types[JSON].Usage())
		if err != nil || p.Name() != name || p.ResultUsage() != shape.Usage() || p.ReturnsSet() != (name == "json_to_recordset") {
			t.Fatal(p, err)
		}
		fresh, _ := jsonRecordUnitTypes(t)
		if _, err := p.Open(context.Background(), []Value{Null(fresh[JSON])}, InputEnvironment{}); err == nil {
			t.Fatal("accepted foreign JSON generation")
		}
		if _, err := p.Open(context.Background(), nil, InputEnvironment{}); err == nil {
			t.Fatal("accepted missing argument")
		}
	}
	if _, err := BindJSONRecord("jsonb_to_record", shape.Usage(), -1, types[JSONB].Usage()); err == nil {
		t.Fatal("admitted adjacent family")
	}
	p, err := BindJSONRecord("json_to_record", DeferredRecord().Usage(), -1, types[JSON].Usage())
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.Evaluate(context.Background(), []Value{Null(types[JSON])}, InputEnvironment{})
	if err != nil || !v.IsNull() || !v.Type().IsDeferredRecord() || v.Type().IsComposite() {
		t.Fatal("NULL fabricated a descriptor", v, err)
	}
	if _, err := p.Evaluate(context.Background(), []Value{jsonRecordUnitJSON(t, types[JSON], "{}")}, InputEnvironment{}); err == nil {
		t.Fatal("undemanded anonymous layout")
	}
	deferred := DeferredRecord().Usage()
	contextual, err := BindJSONRecord("json_populate_record", shape.Usage(), 0, deferred, types[JSON].Usage())
	if err != nil {
		t.Fatal(err)
	}
	value, err := contextual.Evaluate(context.Background(), []Value{Null(deferred.Type), jsonRecordUnitJSON(t, types[JSON], `{"a":2}`)}, InputEnvironment{})
	if err != nil || value.Type() != shape || value.CompositeField(0) != jsonRecordUnitInput(t, types[Int4], "2") || !value.CompositeField(1).IsNull() {
		t.Fatal("contextual NULL RECORD did not use its genuine output descriptor", value, err)
	}
}

func TestJSONRecordConversion(t *testing.T) {
	types, shape := jsonRecordUnitTypes(t)
	base, err := OwnedCompositeValue(shape, []Value{jsonRecordUnitInput(t, types[Int4], "7"), String(types[Text], "keep")})
	if err != nil {
		t.Fatal(err)
	}
	p, err := BindJSONRecord("json_populate_record", shape.Usage(), 0, shape.Usage(), types[JSON].Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		text string
		a    int64
		null bool
		b    string
	}{
		{`{"a":"bad","a":2}`, 2, false, "keep"},
		{`{"\u0061":3,"b":null}`, 3, true, ""},
		{`{}`, 7, false, "keep"},
	} {
		v, err := p.Evaluate(context.Background(), []Value{base, jsonRecordUnitJSON(t, types[JSON], tc.text)}, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		a, err := v.CompositeField(0).Integer()
		if err != nil || a != tc.a || v.CompositeField(1).IsNull() != tc.null {
			t.Fatal(v, err)
		}
		if !tc.null {
			text, err := v.CompositeField(1).Text()
			if err != nil || text != tc.b {
				t.Fatal(text, err)
			}
		}
	}
	arrayShape, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "a", Usage: types[1007].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	arrayPlan, err := BindJSONRecord("json_to_record", arrayShape.Usage(), -1, types[JSON].Usage())
	if err != nil {
		t.Fatal(err)
	}
	v, err := arrayPlan.Evaluate(context.Background(), []Value{jsonRecordUnitJSON(t, types[JSON], `{"a":[[1,2],[3,4]]}`)}, InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	a := v.CompositeField(0)
	if a.ArrayRank() != 2 || a.ArrayDimension(0) != (ArrayDimension{Lower: 1, Length: 2}) || a.ArrayDimension(1) != (ArrayDimension{Lower: 1, Length: 2}) || a.ArrayLen() != 4 {
		t.Fatal("flattened array shape", a)
	}
}
func jsonRecordUnitInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestJSONRecordOwnership(t *testing.T) {
	types, shape := jsonRecordUnitTypes(t)
	t.Run("binary-output", testJSONRecordBinaryOutput)
	p, err := BindJSONRecord("json_populate_recordset", shape.Usage(), 0, shape.Usage(), types[JSON].Usage())
	if err != nil {
		t.Fatal(err)
	}
	base, err := OwnedCompositeValue(shape, []Value{jsonRecordUnitInput(t, types[Int4], "7"), String(types[Text], "keep")})
	if err != nil {
		t.Fatal(err)
	}
	input := jsonRecordUnitJSON(t, types[JSON], `[{"a":2},{"b":"x"}]`)
	c, err := p.Open(context.Background(), []Value{base, input}, InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	first, ok, err := c.Next(context.Background())
	if err != nil || !ok {
		t.Fatal(first, ok, err)
	}
	second, ok, err := c.Next(context.Background())
	if err != nil || !ok {
		t.Fatal(second, ok, err)
	}
	a, _ := second.CompositeField(0).Integer()
	if a != 7 {
		t.Fatal("base accumulated prior row", a)
	}
	c.Close()
	if v, ok, err := c.Next(context.Background()); ok || err != nil || v.Type() != nil {
		t.Fatal("closed cursor retained state", v, ok, err)
	}
	text, err := first.CompositeField(1).Text()
	if err != nil || text != "keep" || first.Type() != shape || base.CompositeField(0) != jsonRecordUnitInput(t, types[Int4], "7") {
		t.Fatal("retained value/base changed", first, base, err)
	}
}

func TestJSONRecordCancellation(t *testing.T) {
	types, shape := jsonRecordUnitTypes(t)
	p, err := BindJSONRecord("json_to_recordset", shape.Usage(), -1, types[JSON].Usage())
	if err != nil {
		t.Fatal(err)
	}
	input := jsonRecordUnitJSON(t, types[JSON], `[{"a":1}]`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Open(ctx, []Value{input}, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	c, err := p.Open(context.Background(), []Value{input}, InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, ok, err := c.Next(ctx); ok || !errors.Is(err, context.Canceled) {
		t.Fatal(ok, err)
	}
	v, ok, err := c.Next(context.Background())
	if err != nil || !ok || v.Type() != shape {
		t.Fatal("canceled advance consumed row", v, ok, err)
	}
}

func testJSONRecordBinaryOutput(t *testing.T) {
	types, _ := jsonRecordUnitTypes(t)
	fresh, _ := jsonRecordUnitTypes(t)
	domain, err := Domain(62001, "record_text", "public.record_text", types[Text].Usage())
	if err != nil {
		t.Fatal(err)
	}
	checked, err := Domain(62002, "checked_text", "public.checked_text", types[Text].Usage(), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		from, to *Descriptor
		accepted bool
	}{
		{types[Text], types[Text], true},
		{types[Text], fresh[Text], false},
		{nil, types[Text], false},
		{types[VarChar], types[Text], true},
		{types[Text], types[VarChar], true},
		{types[BPChar], types[Text], false},
		{types[Int4], types[Int8], false},
		{types[Int4], types[Oid], true},
		{types[Oid], types[Int4], false},
		{domain, domain, true},
		{domain, types[Text], true},
		{domain, fresh[Text], false},
		{types[Text], domain, false},
		{domain, checked, false},
		{types[Text], checked, false},
	} {
		if got := jsonRecordBinaryOutput(tc.from, tc.to); got != tc.accepted {
			t.Fatalf("binary output %v -> %v = %t, want %t", tc.from, tc.to, got, tc.accepted)
		}
	}
	varchar3 := types[VarChar].Usage()
	varchar3.Typmod = 7
	varchar2 := types[VarChar].Usage()
	varchar2.Typmod = 6
	sourceMembers := []CompositeMember{
		{1, "f1", varchar3}, {2, "f2", types[Text].Usage()},
		{3, "f3", varchar3}, {4, "f4", domain.Usage()},
		{5, "f5", domain.Usage()}, {6, "f6", types[Int4].Usage()},
		{7, "f7", types[Int4].Usage()},
	}
	source, err := RecordShape(sourceMembers)
	if err != nil {
		t.Fatal(err)
	}
	target, err := RecordShape([]CompositeMember{
		{1, "a", types[Text].Usage()}, {2, "b", varchar2},
		{3, "c", types[Text].Usage()}, {4, "d", types[Text].Usage()},
		{5, "e", domain.Usage()}, {6, "f", types[Oid].Usage()},
		{7, "g", types[Oid].Usage()},
	})
	if err != nil {
		t.Fatal(err)
	}
	fields := []Value{
		String(types[VarChar], "xyz"), String(types[Text], "long"),
		Null(types[VarChar]), jsonRecordUnitInput(t, domain, "domain"),
		jsonRecordUnitInput(t, domain, "identity"),
		jsonRecordUnitInput(t, types[Int4], "-1"), Null(types[Int4]),
	}
	base, err := OwnedCompositeValue(source, fields)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]Value(nil), fields...)
	plan, err := BindJSONRecord("json_populate_record", target.Usage(), 0, source.Usage(), types[JSON].Usage())
	if err != nil {
		t.Fatal(err)
	}
	input := jsonRecordUnitJSON(t, types[JSON], "{}")
	got, err := plan.Evaluate(context.Background(), []Value{base, input}, InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Type() != target || got.composite == base.composite {
		t.Fatal("output did not own its declared field tags", got)
	}
	for i, original := range before {
		if got.CompositeField(i).Type() != target.CompositeMember(i).Usage.Type {
			t.Fatal("foreign member tag", i, got.CompositeField(i).Type())
		}
		if base.CompositeField(i) != original {
			t.Fatal("relabel mutated the source", i)
		}
	}
	if got.CompositeField(0).text != "xyz" || got.CompositeField(1).text != "long" ||
		!got.CompositeField(2).IsNull() || got.CompositeField(3).text != "domain" ||
		got.CompositeField(4) != before[4] || got.CompositeField(5).oid != 4294967295 ||
		got.CompositeField(5).integer != 0 || !got.CompositeField(6).IsNull() {
		t.Fatal("binary output sized, re-input, or corrupted payload", got)
	}
	if got.Type().CompositeMember(1).Usage != varchar2 || base.Type().CompositeMember(0).Usage != varchar3 {
		t.Fatal("confused original conversion and declared output typmods")
	}
	cursor := JSONRecordCursor{plan: JSONRecordPlan{result: source.Usage()}}
	if unchanged, ok, err := cursor.finish(base); err != nil || !ok || unchanged != base {
		t.Fatal("identity output copied or changed its datum", unchanged, ok, err)
	}
	foreignShape, err := RecordShape(sourceMembers)
	if err != nil {
		t.Fatal(err)
	}
	foreign := base
	foreign.typ = foreignShape
	if _, err := plan.Open(context.Background(), []Value{foreign, input}, InputEnvironment{}); err == nil {
		t.Fatal("accepted a foreign argument occurrence")
	}
	corrupt := base
	corrupt.composite = &compositePayload{fields: append([]Value(nil), before...)}
	corrupt.composite.fields[0] = String(fresh[VarChar], "xyz")
	cursor.plan.result = target.Usage()
	if _, ok, err := cursor.finish(corrupt); err == nil || ok {
		t.Fatal("reconciliation hid a foreign source member generation")
	}
}
