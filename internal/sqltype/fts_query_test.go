package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestFTSQueryFunctionsTypedAuthority(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		op   FTSQueryFunction
		want string
	}{
		{FTSNumNode, "1"}, {FTSQueryTree, "'a'"}, {FTSRewrite, "'b'"},
	} {
		var args [3]Usage
		var values [3]Value
		for i := range tc.op.Arity() {
			args[i] = types[tc.op.Formal(i)].Usage()
			input := "a"
			if i == 2 {
				input = "b"
			}
			v, err := args[i].Type.Input(ctx, input, -1)
			if err != nil {
				t.Fatal(err)
			}
			values[i] = v
		}
		p, err := BindFTSQuery(tc.op, types[tc.op.ResultID()].Usage(), args)
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Evaluate(ctx, values)
		if err != nil || v.Type() != types[tc.op.ResultID()] {
			t.Fatal(tc.op, v, err)
		}
		text, err := v.Type().Output(ctx, v)
		if err != nil || text != tc.want {
			t.Fatal(tc.op, text, err)
		}
		for i := range tc.op.Arity() {
			nulls := values
			nulls[i] = Null(args[i].Type)
			v, err := p.Evaluate(ctx, nulls)
			if err != nil || !v.IsNull() || v.Type() != p.result {
				t.Fatal("strict typed NULL", err)
			}
		}
		foreign := testTypes()
		if _, err := BindFTSQuery(tc.op, foreign[tc.op.ResultID()].Usage(), args); err == nil {
			t.Fatal("foreign result accepted")
		}
		bad := values
		bad[0] = Value{typ: types[TSQuery]}
		if _, err := p.Evaluate(ctx, bad); err == nil {
			t.Fatal("forged payload accepted")
		}
		bad = values
		bad[0], err = foreign[TSQuery].Input(ctx, "a", -1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Evaluate(ctx, bad); err == nil {
			t.Fatal("foreign value accepted")
		}
		forged := p
		forged.inputs[0] = foreign[TSQuery]
		if err := forged.ValidateValues(values); err == nil {
			t.Fatal("forged plan accepted")
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := p.Evaluate(canceled, values); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if _, err := BindFTSQuery(FTSQueryFunction(255), types[TSQuery].Usage(), [3]Usage{}); err == nil {
		t.Fatal("invalid function accepted")
	}
}

func TestFTSQueryFunctionsTypedRules(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[TSQuery]
	value := func(text string) Value {
		v, err := d.Input(ctx, text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	initial := value("a")
	rules, err := NewFTSRewriteRules(ctx, initial, d)
	if err != nil {
		t.Fatal(err)
	}
	if empty, err := rules.Empty(); err != nil || empty {
		t.Fatal("nonempty checked accumulator", empty, err)
	}
	for _, row := range [][2]Value{{value("a"), Null(d)}, {Null(d), value("z")}, {value(""), value("z")}, {value("a"), value("b")}, {value("b"), value("c")}} {
		if err := rules.Apply(row[0], row[1]); err != nil {
			t.Fatal(err)
		}
		if empty, err := rules.Empty(); err != nil || empty {
			t.Fatal("NULL/empty targets and nondeleting rules preserve nonempty state", empty, err)
		}
	}
	result, err := rules.Finish()
	if err != nil || result.ftsQuery.String() != "'c'" || initial.ftsQuery.String() != "'a'" {
		t.Fatal(result, err)
	}
	foreign := testTypes()[TSQuery]
	v, err := foreign.Input(ctx, "a", -1)
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.Apply(v, value("z")); err == nil {
		t.Fatal("foreign rule accepted")
	}
	if _, err := NewFTSRewriteRules(ctx, initial, foreign); err == nil {
		t.Fatal("foreign accumulator accepted")
	}
	rules, err = NewFTSRewriteRules(ctx, initial, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.Apply(value("a"), value("")); err != nil {
		t.Fatal(err)
	}
	if empty, err := rules.Empty(); err != nil || !empty {
		t.Fatal("deleted accumulator state", empty, err)
	}
	for _, invalid := range []*FTSRewriteRules{nil, {}, {owner: types[Text], workspace: rules.workspace}} {
		if _, err := invalid.Empty(); err == nil {
			t.Fatal("invalid accumulator state authority accepted")
		}
	}
	result, err = rules.Finish()
	if err != nil || !result.ftsQuery.Empty() || initial.ftsQuery.String() != "'a'" {
		t.Fatal("empty substitute is deletion", err)
	}
}
