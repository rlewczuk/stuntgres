package sqltype

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func jsonbRecordUnitType(t *testing.T, id ID, members ...CompositeMember) *Descriptor {
	t.Helper()
	d, err := Composite(id, "jbr_unit", "jbr_unit", id+1, members)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func jsonbRecordUnitInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func jsonbRecordUnitOutput(t *testing.T, d *Descriptor, v Value) string {
	t.Helper()
	text, err := d.Output(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	return text
}
func TestJSONBRecordConversion(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	pair := jsonbRecordUnitType(t, 26000, CompositeMember{1, "a", types[Int4].Usage()}, CompositeMember{3, "b", types[Text].Usage()})
	base := jsonbRecordUnitInput(t, pair, "(9,keep)")
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{}`, "(9,keep)"}, {`{"a":1}`, "(1,keep)"}, {`{"a":null}`, "(,keep)"}, {`{"b":"x","ignored":"bad","a":1,"a":2}`, "(2,x)"},
	} {
		doc := jsonbRecordUnitInput(t, types[JSONB], tc.input)
		v, err := JSONBPopulateRecord(ctx, pair.Usage(), base, doc, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if got := jsonbRecordUnitOutput(t, pair, v); got != tc.want {
			t.Fatalf("%s got=%q want=%q", tc.input, got, tc.want)
		}
		if tc.input == `{}` && v.composite != base.composite {
			t.Fatal("unchanged base copied")
		}
		if got := jsonbRecordUnitOutput(t, pair, base); got != "(9,keep)" {
			t.Fatal("base mutated", got)
		}
	}
	v, err := JSONBPopulateRecord(ctx, pair.Usage(), Null(pair), jsonbRecordUnitInput(t, types[JSONB], `{}`), InputEnvironment{})
	if err != nil || v.IsNull() || v.CompositeLen() != 2 || !v.CompositeField(0).IsNull() || !v.CompositeField(1).IsNull() {
		t.Fatal("all-null payload lost", v, err)
	}
	v, err = JSONBPopulateRecord(ctx, pair.Usage(), Null(pair), Null(types[JSONB]), InputEnvironment{})
	if err != nil || !v.IsNull() || v.composite != nil {
		t.Fatal("whole NULL lost", v, err)
	}
	for _, input := range []string{`null`, `[]`, `1`, `"x"`} {
		_, err := JSONBPopulateRecord(ctx, pair.Usage(), Null(pair), jsonbRecordUnitInput(t, types[JSONB], input), InputEnvironment{})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22023" || !JSONBRecordSoftError(err) {
			t.Fatal(input, err)
		}
	}
	// Errors follow declaration order even when JSONB's key order is opposite.
	ordered := jsonbRecordUnitType(t, 26010, CompositeMember{1, "z", types[Int4].Usage()}, CompositeMember{2, "a", types[Int4].Usage()})
	_, err = JSONBPopulateRecord(ctx, ordered.Usage(), Null(ordered), jsonbRecordUnitInput(t, types[JSONB], `{"a":"second","z":"first"}`), InputEnvironment{})
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Message != `invalid input syntax for type integer: "first"` {
		t.Fatal(err)
	}
	foreign := jsonbRecordUnitType(t, 26020, CompositeMember{1, "a", types[Int4].Usage()}, CompositeMember{2, "b", types[Text].Usage()})
	if _, err := JSONBPopulateRecord(ctx, pair.Usage(), Null(foreign), Null(types[JSONB]), InputEnvironment{}); err == nil || JSONBRecordSoftError(err) {
		t.Fatal("foreign descriptor accepted", err)
	}
	if _, err := OwnedCompositeValue(pair, []Value{Null(types[Text]), Null(types[Text])}); err == nil {
		t.Fatal("foreign member admitted")
	}
	rows, err := JSONBPopulateRecordset(ctx, "jsonb_populate_recordset", pair.Usage(), base, jsonbRecordUnitInput(t, types[JSONB], `[{"a":1},{"b":"x"},{}]`), InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	var outputs []string
	for _, row := range rows {
		outputs = append(outputs, jsonbRecordUnitOutput(t, pair, row))
	}
	if !reflect.DeepEqual(outputs, []string{"(1,keep)", "(9,x)", "(9,keep)"}) {
		t.Fatal(outputs)
	}
	for _, input := range []string{`[{"a":1},null]`, `[{"a":1},{"a":"bad"}]`} {
		rows, err := JSONBPopulateRecordset(ctx, "jsonb_populate_recordset", pair.Usage(), base, jsonbRecordUnitInput(t, types[JSONB], input), InputEnvironment{})
		if err == nil || rows != nil {
			t.Fatal("partial recordset published", rows, err)
		}
	}
}

type jsonbRecordDomainRule struct{ calls int }

func (r *jsonbRecordDomainRule) CheckDomain(ctx context.Context, d *Descriptor, v Value) error {
	r.calls++
	if err := ctx.Err(); err != nil {
		return err
	}
	if v.IsNull() {
		return &sqlstate.Error{Code: "23514", Message: "unit required domain"}
	}
	n, err := v.Integer()
	if err != nil {
		return err
	}
	if n <= 0 {
		return &sqlstate.Error{Code: "23514", Message: "unit positive domain"}
	}
	return nil
}
func TestJSONBRecordArraysDomains(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	array, err := Array(26100, "_jbr_unit_int", types[Int4])
	if err != nil {
		t.Fatal(err)
	}
	inner := jsonbRecordUnitType(t, 26110, CompositeMember{1, "a", types[Int4].Usage()}, CompositeMember{2, "b", types[Text].Usage()})
	outer := jsonbRecordUnitType(t, 26120, CompositeMember{1, "r", inner.Usage()}, CompositeMember{2, "a", array.Usage()}, CompositeMember{3, "j", types[JSONB].Usage()})
	oldInner := jsonbRecordUnitInput(t, inner, "(9,keep)")
	base, err := OwnedCompositeValue(outer, []Value{oldInner, Null(array), Null(types[JSONB])})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ doc, array string }{
		{`{"r":{"a":1},"a":[1,"2",null],"j":{"nested":[1,2]}}`, `{1,2,NULL}`},
		{`{"r":{"a":1},"a":[[1,2],[3,4]]}`, `{{1,2},{3,4}}`},
		{`{"r":{"a":1},"a":"[0:1]={7,8}"}`, `[0:1]={7,8}`},
	} {
		doc := jsonbRecordUnitInput(t, types[JSONB], tc.doc)
		v, err := JSONBPopulateRecord(ctx, outer.Usage(), base, doc, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if got := jsonbRecordUnitOutput(t, inner, v.CompositeField(0)); got != "(1,keep)" {
			t.Fatal(got)
		}
		if got := jsonbRecordUnitOutput(t, array, v.CompositeField(1)); got != tc.array {
			t.Fatal(got, tc.array)
		}
		if !v.CompositeField(2).IsNull() {
			node, err := jsonbExtractField(ctx, &doc.jsonb.root, "j")
			if err != nil {
				t.Fatal(err)
			}
			if &node.fields[0].value.items[0] != &v.CompositeField(2).jsonb.root.fields[0].value.items[0] {
				t.Fatal("immutable child nodes copied")
			}
		}
	}
	for _, tc := range []struct{ doc, message, hint, detail string }{
		{`{"a":[[1],2]}`, "expected JSON array", `See the array element [1] of key "a".`, ""},
		{`{"a":[[1],[2,3]]}`, "malformed JSON array", "", "Multidimensional arrays must have sub-arrays with matching dimensions."},
	} {
		_, err := JSONBPopulateRecord(ctx, outer.Usage(), base, jsonbRecordUnitInput(t, types[JSONB], tc.doc), InputEnvironment{})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22P02" || d.Message != tc.message || d.Hint != tc.hint || d.Detail != tc.detail {
			t.Fatal(err)
		}
	}
	domain, err := Domain(26130, "jbr_unit_positive", "jbr_unit_positive", types[Int4].Usage(), true)
	if err != nil {
		t.Fatal(err)
	}
	row := jsonbRecordUnitType(t, 26140, CompositeMember{1, "d", domain.Usage()})
	rule := &jsonbRecordDomainRule{}
	env := InputEnvironment{DomainChecks: rule}
	for _, doc := range []string{`{}`, `{"d":null}`, `{"d":0}`} {
		_, err := JSONBPopulateRecord(ctx, row.Usage(), Null(row), jsonbRecordUnitInput(t, types[JSONB], doc), env)
		if !JSONBRecordSoftError(err) {
			t.Fatal("missing fresh domain check", doc, err)
		}
	}
	physical := jsonbRecordUnitInput(t, types[Int4], "7")
	wrapped, err := domain.Wrap(physical)
	if err != nil {
		t.Fatal(err)
	}
	old, err := OwnedCompositeValue(row, []Value{wrapped})
	if err != nil {
		t.Fatal(err)
	}
	before := rule.calls
	v, err := JSONBPopulateRecord(ctx, row.Usage(), old, jsonbRecordUnitInput(t, types[JSONB], `{}`), env)
	if err != nil || v.composite != old.composite || rule.calls != before {
		t.Fatal("retained valid base rechecked/copied", err, rule.calls, before)
	}
}

func TestJSONBRecordCancellation(t *testing.T) {
	types := testTypes()
	array, err := Array(26200, "_jbr_cancel", types[Int4])
	if err != nil {
		t.Fatal(err)
	}
	row := jsonbRecordUnitType(t, 26210, CompositeMember{1, "a", array.Usage()})
	items := make([]jsonbNode, 1000)
	for i := range items {
		items[i] = jsonbNode{kind: jsonbNumber, number: jsonbDecimal{digits: "1"}}
	}
	doc := Value{typ: types[JSONB], jsonb: &jsonbPayload{root: jsonbNode{kind: jsonbObject, fields: []jsonbEntry{{key: "a", value: jsonbNode{kind: jsonbArray, items: items}}}}}}
	ctx := &jsonCreateWorkContext{Context: context.Background(), remaining: 30}
	v, err := JSONBPopulateRecord(ctx, row.Usage(), Null(row), doc, InputEnvironment{})
	if !errors.Is(err, context.Canceled) || v.typ != nil || JSONBRecordSoftError(err) {
		t.Fatal("conversion cancellation swallowed/published", v, err)
	}
	ctx = &jsonCreateWorkContext{Context: context.Background(), remaining: 40}
	doc.jsonb = &jsonbPayload{root: jsonbNode{kind: jsonbArray, items: []jsonbNode{doc.jsonb.root, doc.jsonb.root}}}
	rows, err := JSONBPopulateRecordset(ctx, "jsonb_populate_recordset", row.Usage(), Null(row), doc, InputEnvironment{})
	if !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal("recordset cancellation published", rows, err)
	}
}
