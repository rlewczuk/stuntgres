package proc

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRoutineResultSchema(t *testing.T) {
	integer := routineTestType(t, sqltype.Int4)
	fs := []Formal{{Mode: "o", Usage: integer.Usage()}, {Mode: "i", Name: "input", Usage: integer.Usage()}, {Mode: "b", Name: "both", Usage: integer.Usage()}, {Mode: "t", Name: "last", Usage: integer.Usage()}}
	d := &Definition{}
	if err := d.SetFormals(fs); err != nil {
		t.Fatal(err)
	}
	shape, err := d.OutputTuple()
	if err != nil {
		t.Fatal(err)
	}
	if !d.MatchesOutputTuple(shape) || !d.DerivedResult() || !reflect.DeepEqual(d.InputFormals, []int{1, 2}) || !reflect.DeepEqual(d.OutputFormals, []int{0, 2, 3}) || !reflect.DeepEqual(d.FormalInputs, []int{-1, 0, 1, -1}) {
		t.Fatal("output/input projection", d)
	}
	for i, name := range []string{"column1", "both", "last"} {
		m := shape.CompositeMember(i)
		if m.Name != name || m.Attribute != int32(i+1) || m.Usage != integer.Usage() {
			t.Fatal("ordered output", m)
		}
	}
	fs[0].Name = "mutated"
	if shape.CompositeMember(0).Name != "column1" || d.Formals[0].Name != "" {
		t.Fatal("borrowed declaration storage")
	}
	composite, err := sqltype.Composite(98000, "pair", "pair", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "x", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	one := &Definition{}
	if err := one.SetFormals([]Formal{{Mode: "o", Name: "answer", Usage: composite.Usage()}}); err != nil {
		t.Fatal(err)
	}
	tuple, err := one.OutputTuple()
	if err != nil {
		t.Fatal(err)
	}
	if one.DerivedResult() || one.OutputName() != "answer" || tuple.CompositeLen() != 1 || tuple.CompositeMember(0).Usage != composite.Usage() {
		t.Fatal("single composite OUT collapsed tuple")
	}
	plan, err := PlanTuple([]sqltype.Usage{composite.Usage()}, tuple)
	if err != nil {
		t.Fatal(err)
	}
	field, err := sqltype.Integer(integer, 7)
	if err != nil {
		t.Fatal(err)
	}
	value, err := sqltype.CompositeValue(composite, []sqltype.Value{field})
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.Apply(context.Background(), []sqltype.Value{value}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Type() != tuple || got.CompositeLen() != 1 || got.CompositeField(0).Type() != composite || !sqltype.SameDatum(got.CompositeField(0), value) {
		t.Fatal("tuple wrapper lost composite output")
	}
}

func TestRoutineResultAdaptation(t *testing.T) {
	ctx := context.Background()
	integer, wide, text, bytes := routineTestType(t, sqltype.Int4), routineTestType(t, sqltype.Int8), routineTestType(t, sqltype.Text), routineTestType(t, sqltype.Bytea)
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "big", Usage: wide.Usage()}, {Attribute: 2, Name: "payload", Usage: bytes.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanResult([]sqltype.Usage{integer.Usage(), bytes.Usage()}, shape.Usage())
	if err != nil {
		t.Fatal(err)
	}
	number, err := sqltype.Integer(integer, 5)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := bytes.Input(ctx, `\x0001ff`, -1)
	if err != nil {
		t.Fatal(err)
	}
	row := []sqltype.Value{number, payload}
	value, err := plan.Apply(ctx, row, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	row[0], row[1] = sqltype.Null(integer), sqltype.Null(bytes)
	frame := NewFrame(nil)
	owned, err := frame.Result(value, shape.Usage())
	if err != nil {
		t.Fatal(err)
	}
	frame.Close()
	n, err := owned.CompositeField(0).Integer()
	if err != nil || n != 5 || owned.CompositeField(0).Type() != wide {
		t.Fatal("ordinal assignment conversion", n, err)
	}
	encoded, err := bytes.Output(ctx, owned.CompositeField(1))
	if err != nil || encoded != `\x0001ff` {
		t.Fatal("owned nested bytes", encoded, err)
	}
	nulls, err := plan.Apply(ctx, []sqltype.Value{sqltype.Null(integer), sqltype.Null(bytes)}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil || nulls.IsNull() || nulls.CompositeLen() != 2 {
		t.Fatal("present all-NULL tuple became absent", err)
	}
	if !sqltype.Null(shape).IsNull() {
		t.Fatal("absent tuple lost whole NULL")
	}
	whole, err := PlanResult([]sqltype.Usage{shape.Usage()}, shape.Usage())
	if err != nil {
		t.Fatal(err)
	}
	same, err := whole.Apply(ctx, []sqltype.Value{owned}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil || !sqltype.SameDatum(same, owned) {
		t.Fatal("whole composite identity", err)
	}
	domain, err := sqltype.Domain(98001, "result_domain", "result_domain", shape.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanResult([]sqltype.Usage{integer.Usage(), bytes.Usage()}, domain.Usage()); err == nil {
		t.Fatal("composite domain unpacked")
	}
	domainPlan, err := PlanResult([]sqltype.Usage{shape.Usage()}, domain.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := domainPlan.Apply(ctx, []sqltype.Value{owned}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		columns []sqltype.Usage
		result  sqltype.Usage
		detail  string
	}{
		{nil, integer.Usage(), "Final statement must return exactly one column."},
		{[]sqltype.Usage{integer.Usage(), integer.Usage()}, integer.Usage(), "Final statement must return exactly one column."},
		{[]sqltype.Usage{text.Usage()}, integer.Usage(), "Actual return type is text."},
		{[]sqltype.Usage{integer.Usage()}, shape.Usage(), "Final statement returns too few columns."},
		{[]sqltype.Usage{integer.Usage(), bytes.Usage(), integer.Usage()}, shape.Usage(), "Final statement returns too many columns."},
		{[]sqltype.Usage{integer.Usage(), text.Usage()}, shape.Usage(), "Final statement returns text instead of bytea at column 2."},
	} {
		_, err := PlanResult(tc.columns, tc.result)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P13" || diagnostic.Detail != tc.detail || diagnostic.Context != "" {
			t.Fatal("result schema diagnostic", err, tc.detail)
		}
		if tc.result.Type.ID() == 2249 && diagnostic.Message != "return type mismatch in function declared to return record" {
			t.Fatal("anonymous record diagnostic name", diagnostic.Message)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := plan.Apply(canceled, []sqltype.Value{number, payload}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal("conversion cancellation", err)
	}
}
