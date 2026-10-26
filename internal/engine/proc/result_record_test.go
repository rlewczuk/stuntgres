package proc

import (
	"context"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestPseudoRecordResultPlan(t *testing.T) {
	ctx := context.Background()
	integer, bytes := routineTestType(t, sqltype.Int4), routineTestType(t, sqltype.Bytea)
	record := sqltype.DeferredRecord()
	columns := []sqltype.CompositeMember{{Attribute: 1, Name: "actual_number", Usage: integer.Usage()}, {Attribute: 2, Name: "actual_payload", Usage: bytes.Usage()}}
	plan, err := PlanNamedResult(columns, record.Usage())
	if err != nil {
		t.Fatal(err)
	}
	columns[0].Name = "mutated"
	number, err := sqltype.Integer(integer, 7)
	if err != nil {
		t.Fatal(err)
	}
	single, err := PlanNamedResult([]sqltype.CompositeMember{{Attribute: 1, Name: "single_number", Usage: integer.Usage()}}, record.Usage())
	if err != nil {
		t.Fatal(err)
	}
	packed, err := single.Apply(ctx, []sqltype.Value{number}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil || packed.Type() != record || packed.IsNull() || packed.ActualRecord().Type().CompositeLen() != 1 || packed.ActualRecord().Type().CompositeMember(0).Name != "single_number" || !sqltype.SameDatum(packed.CompositeField(0), number) {
		t.Fatal("single scalar was not packed into its named record", err)
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
	actual := value.ActualRecord()
	if value.Type() != record || value.IsNull() || actual.Type().ID() != 2249 || actual.Type().CompositeLen() != 2 || actual.Type().CompositeMember(0).Name != "actual_number" || actual.Type().CompositeMember(1).Name != "actual_payload" || actual.Type().CompositeMember(0).Usage != integer.Usage() || actual.Type().CompositeMember(1).Usage != bytes.Usage() {
		t.Fatal("projected shape lost")
	}
	got, err := actual.CompositeField(0).Integer()
	if err != nil || got != 7 || !sqltype.SameDatum(actual.CompositeField(1), payload) {
		t.Fatal("owned tuple lost source values", got, err)
	}
	encoded, err := bytes.Output(ctx, actual.CompositeField(1))
	if err != nil || encoded != `\x0001ff` {
		t.Fatal("owned tuple payload", encoded, err)
	}
	presentNulls, err := plan.Apply(ctx, row, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil || presentNulls.IsNull() || presentNulls.ActualRecord().Type() != actual.Type() || !presentNulls.CompositeField(0).IsNull() || !presentNulls.CompositeField(1).IsNull() {
		t.Fatal("all-NULL tuple became absent", err)
	}
	for _, source := range []sqltype.Value{actual, value, sqltype.Null(actual.Type())} {
		whole, err := PlanNamedResult([]sqltype.CompositeMember{{Attribute: 1, Name: "ignored_outer_label", Usage: source.Type().Usage()}}, record.Usage())
		if err != nil {
			t.Fatal(err)
		}
		got, err := whole.Apply(ctx, []sqltype.Value{source}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
		if err != nil || got.Type() != record || got.IsNull() != source.IsNull() {
			t.Fatal("whole record transport", err)
		}
		if !source.IsNull() && !sqltype.SameDatum(got.ActualRecord(), actual) {
			t.Fatal("whole record copied or erased actual payload")
		}
	}
	if !sqltype.Null(record).IsNull() {
		t.Fatal("caller-owned absent row lost NULL")
	}
	text := routineTestType(t, sqltype.Text)
	target, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "converted", Usage: text.Usage()}, {Attribute: 2, Name: "payload", Usage: bytes.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	conversion, err := PlanNamedResult([]sqltype.CompositeMember{{Attribute: 1, Name: "number", Usage: integer.Usage()}, {Attribute: 2, Name: "bytes", Usage: bytes.Usage()}}, target.Usage())
	if err != nil {
		t.Fatal(err)
	}
	converted, err := conversion.Apply(ctx, []sqltype.Value{number, payload}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	output, err := text.Output(ctx, converted.CompositeField(0))
	if err != nil || output != "7" || converted.Type() != target {
		t.Fatal("shaped FROM assignment conversion", output, err)
	}
}
