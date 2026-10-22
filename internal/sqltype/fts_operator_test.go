package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestFTSOpsTypedPlan(t *testing.T) {
	types := map[ID]*Descriptor{}
	for _, d := range Builtins() {
		types[d.id] = d
	}
	ctx := context.Background()
	for _, tc := range []struct {
		op      FTSOperator
		want    string
		boolean bool
	}{
		{FTSAnd, "'a' & 'b'", false}, {FTSOr, "'a' | 'b'", false}, {FTSNot, "!'a'", false},
		{FTSPhrase, "'a' <-> 'b'", false}, {FTSPhraseDistance, "'a' <2> 'b'", false},
		{FTSContains, "", false}, {FTSContained, "", false}, {FTSConcat, "'a' 'b'", false},
	} {
		op := tc.op
		var usages [3]Usage
		var values [3]Value
		for i := range op.Arity() {
			usages[i] = types[op.Formal(i)].Usage()
			input := "a"
			if i == 1 {
				input = "b"
			}
			if i == 2 {
				input = "2"
			}
			var err error
			values[i], err = usages[i].Type.Input(ctx, input, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		p, err := BindFTS(op, types[op.ResultID()].Usage(), usages)
		if err != nil {
			t.Fatal(op, err)
		}
		before := values
		result, err := p.Evaluate(ctx, values)
		if err != nil || result.Type() != types[op.ResultID()] || result.IsNull() {
			t.Fatal(op, result, err)
		}
		if op.ResultID() == Bool {
			if result.boolean != tc.boolean {
				t.Fatal(op, result.boolean)
			}
		} else {
			out, err := result.Type().Output(ctx, result)
			if err != nil || out != tc.want {
				t.Fatal(op, out, err)
			}
		}
		if values != before {
			t.Fatal("mutated value slots")
		}
		for i := range op.Arity() {
			nullable := values
			nullable[i] = Null(usages[i].Type)
			got, err := p.Evaluate(ctx, nullable)
			if err != nil || !got.IsNull() || got.Type() != types[op.ResultID()] {
				t.Fatal("typed NULL", op, i, got, err)
			}
		}
		invalid := values
		invalid[0] = Value{}
		if _, err := p.Evaluate(ctx, invalid); err == nil {
			t.Fatal("zero operand accepted", op)
		}
		invalid[0] = String(types[Text], "a")
		if _, err := p.Evaluate(ctx, invalid); err == nil {
			t.Fatal("typed TEXT accepted", op)
		}
		wrong := usages
		wrong[0] = types[Text].Usage()
		if _, err := BindFTS(op, types[op.ResultID()].Usage(), wrong); err == nil {
			t.Fatal("wrong operand signature", op)
		}
		foreign := ftsUnitType(t, op.ResultID())
		if _, err := BindFTS(op, foreign.Usage(), usages); err == nil {
			t.Fatal("foreign result authority", op)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := p.Evaluate(canceled, values); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	query := types[TSQuery]
	distance := types[Int4]
	usages := [3]Usage{query.Usage(), query.Usage(), distance.Usage()}
	p, err := BindFTS(FTSPhraseDistance, query.Usage(), usages)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := query.Input(ctx, "", -1)
	if err != nil {
		t.Fatal(err)
	}
	a, err := query.Input(ctx, "a", -1)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := Integer(distance, 16385)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Evaluate(ctx, [3]Value{empty, a, bad})
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "22023" {
		t.Fatal("empty suppressed invalid distance", err)
	}
	result, err := p.Evaluate(ctx, [3]Value{Null(query), a, bad})
	if err != nil || !result.IsNull() {
		t.Fatal("NULL distance strictness", err)
	}
	if _, err := BindFTS(FTSOperator(255), query.Usage(), usages); err == nil {
		t.Fatal("invalid operation accepted")
	}
}
