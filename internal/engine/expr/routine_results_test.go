package expr

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// Exercise the real tuple/frame/source ownership boundaries without a second
// routine executor or a mock SQL host. Statement effects are reference coverage.
func TestSQLRoutineSetOwnership(t *testing.T) {
	ctx := context.Background()
	integer := sourceCursorType(t, sqltype.Int4)
	bytes := sourceCursorType(t, sqltype.Bytea)
	definition := &proc.Definition{ReturnsSet: true}
	if err := definition.SetFormals([]proc.Formal{{Mode: "i", Usage: integer.Usage()}, {Mode: "o", Name: "n", Usage: integer.Usage()}, {Mode: "o", Name: "bytes", Usage: bytes.Usage()}}); err != nil {
		t.Fatal(err)
	}
	shape, err := definition.OutputTuple()
	if err != nil {
		t.Fatal(err)
	}
	definition.Result = shape.Usage()
	conversion, err := proc.PlanResult([]sqltype.Usage{integer.Usage(), bytes.Usage()}, definition.Result)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := bytes.Input(ctx, `\x0102`, -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{0, 1, 3} {
		frame := proc.NewFrame(definition.Inputs)
		values := make([]sqltype.Value, 0, count)
		for i := range count {
			number, err := sqltype.Integer(integer, int64(i+1))
			if err != nil {
				t.Fatal(err)
			}
			if err := frame.Bind(0, number, integer.Usage()); err != nil {
				t.Fatal(err)
			}
			value, err := conversion.Apply(ctx, []sqltype.Value{frame.Parameters[0], payload}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
		cursor := OwnedSource(shape, values)
		frame.Close()
		for i := range count {
			value, present, err := cursor.Next(ctx)
			if err != nil || !present || value.Type() != shape {
				t.Fatal("private set row", i, present, err)
			}
			number, err := value.CompositeField(0).Integer()
			if err != nil || number != int64(i+1) {
				t.Fatal("frame was reused by returned tuple", number, err)
			}
			encoded, err := bytes.Output(ctx, value.CompositeField(1))
			if err != nil || encoded != `\x0102` {
				t.Fatal("nested byte payload lifetime", encoded, err)
			}
		}
		value, present, err := cursor.Next(ctx)
		if err != nil || present || !value.IsNull() || value.Type() != shape || cursor.Values != nil {
			t.Fatal("empty/EOF cardinality and release", present, err)
		}
		cursor.Close()
		cursor.Close()
	}
	one, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	record, err := conversion.Apply(ctx, []sqltype.Value{one, payload}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	first, second := OwnedSource(shape, []sqltype.Value{record, record}), OwnedSource(shape, []sqltype.Value{record})
	retained, present, err := first.Next(ctx)
	if err != nil || !present {
		t.Fatal(err)
	}
	first.Close()
	first.Close()
	if !sqltype.SameDatum(retained, record) || first.Values != nil {
		t.Fatal("partial close invalidated returned tuple")
	}
	if _, present, err := second.Next(ctx); err != nil || !present {
		t.Fatal("closing sibling invalidated independent cursor", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, present, err := second.Next(canceled); present || !errors.Is(err, context.Canceled) || second.Values != nil {
		t.Fatal("canceled cursor retains invocation", present, err)
	}
	fresh := OwnedSource(shape, []sqltype.Value{record})
	if _, present, err := fresh.Next(ctx); err != nil || !present {
		t.Fatal("cancellation poisoned next invocation", err)
	}
	fresh.Close()
}
