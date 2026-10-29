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

func TestFTSMatchTypedPlan(t *testing.T) {
	types, foreign := map[ID]*Descriptor{}, map[ID]*Descriptor{}
	for _, d := range Builtins() {
		types[d.id] = d
	}
	for _, d := range Builtins() {
		foreign[d.id] = d
	}
	input := func(d *Descriptor, text string) Value {
		v, err := d.Input(t.Context(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for op := FTSMatchVQ; op <= FTSMatchTQ; op++ {
		usages := [3]Usage{types[op.Formal(0)].Usage(), types[op.Formal(1)].Usage()}
		p, err := BindFTS(op, types[Bool].Usage(), usages)
		if err != nil {
			t.Fatal(op, err)
		}
		values := [3]Value{input(usages[0].Type, "cat"), input(usages[1].Type, "cat")}
		for i := range 2 {
			wrong := usages
			wrong[i] = foreign[op.Formal(i)].Usage()
			if _, err := BindFTS(op, types[Bool].Usage(), wrong); err == nil {
				t.Fatal("foreign input accepted", op, i)
			}
			wrongValues := values
			wrongValues[i] = Null(foreign[op.Formal(i)])
			if _, err := p.Evaluate(t.Context(), wrongValues); err == nil {
				t.Fatal("foreign NULL accepted", op, i)
			}
		}
		if _, err := BindFTS(op, foreign[Bool].Usage(), usages); err == nil {
			t.Fatal("foreign result", op)
		}
		if op == FTSMatchTT || op == FTSMatchTQ {
			if _, err := p.Evaluate(t.Context(), values); err == nil {
				t.Fatal("text reinterpreted", op)
			}
			if null, err := p.MatchInputs(t.Context(), values); err != nil || null {
				t.Fatal(null, err)
			}
			vector, err := p.MatchConversionTarget(0)
			if err != nil || vector != types[TSVector] {
				t.Fatal(vector, err)
			}
			if op == FTSMatchTT {
				query, err := p.MatchConversionTarget(1)
				if err != nil || query != types[TSQuery] {
					t.Fatal(query, err)
				}
			} else if _, err := p.MatchConversionTarget(1); err == nil {
				t.Fatal("typed query conversion admitted")
			}
			if _, err := p.MatchConversionTarget(2); err == nil {
				t.Fatal("extra conversion target")
			}
			v, q := input(types[TSVector], "cat"), input(types[TSQuery], "cat")
			result, err := p.EvaluateNormalizedMatch(t.Context(), v, q)
			if err != nil || result.Type() != types[Bool] || !result.boolean {
				t.Fatal(result, err)
			}
			for _, pair := range [][2]Value{
				{input(foreign[TSVector], "cat"), q}, {v, input(foreign[TSQuery], "cat")},
				{Null(foreign[TSVector]), q}, {v, Value{typ: types[TSQuery]}},
				{Value{}, q}, {String(types[Text], "cat"), q},
			} {
				if _, err := p.EvaluateNormalizedMatch(t.Context(), pair[0], pair[1]); err == nil {
					t.Fatal("normalized authority/payload accepted", op)
				}
			}
		} else {
			result, err := p.Evaluate(t.Context(), values)
			if err != nil || !result.boolean {
				t.Fatal(result, err)
			}
			bad := values
			bad[0] = Value{typ: usages[0].Type}
			if _, err := p.Evaluate(t.Context(), bad); err == nil {
				t.Fatal("invalid payload")
			}
		}
		for i := range 2 {
			nullable := values
			nullable[i] = Null(usages[i].Type)
			result, err := p.Evaluate(t.Context(), nullable)
			if err != nil || !result.IsNull() || result.Type() != types[Bool] {
				t.Fatal("strict NULL", op, i, result, err)
			}
			if op == FTSMatchTT || op == FTSMatchTQ {
				if null, err := p.MatchInputs(t.Context(), nullable); err != nil || !null {
					t.Fatal(null, err)
				}
			}
		}
		zero := values
		zero[0] = Value{}
		if _, err := p.Evaluate(t.Context(), zero); err == nil {
			t.Fatal("zero operand", op)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := p.Evaluate(ctx, values); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if op == FTSMatchTT || op == FTSMatchTQ {
			if _, err := p.MatchInputs(ctx, values); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := p.EvaluateNormalizedMatch(ctx, input(types[TSVector], "cat"), input(types[TSQuery], "cat")); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
	if _, ok := FTSKind("@@", 2, false); ok {
		t.Fatal("spelling selected mixed signature")
	}
	if _, ok := FTSKind("@@@", 2, false); ok {
		t.Fatal("deprecated operator activated")
	}
}
