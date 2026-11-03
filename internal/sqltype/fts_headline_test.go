package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/fts"
)

func TestFTSHeadlineTypedPlan(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	for _, explicit := range []bool{false, true} {
		for _, options := range []bool{false, true} {
			op := FTSHeadlineFunction{explicit, options}
			var usages [4]Usage
			var values [4]Value
			for i := range op.Arity() {
				id := op.Formal(i)
				usages[i] = types[id].Usage()
				if id == Regconfig {
					values[i], _ = RegTSValue(types[id], 13164)
					continue
				}
				input := "Cat"
				if id == TSQuery {
					input = "cat"
				}
				if options && i == op.Arity()-1 {
					input = "HighlightAll=true"
				}
				value, err := types[id].Input(t.Context(), input, -1)
				if err != nil {
					t.Fatal(err)
				}
				values[i] = value
			}
			plan, err := BindFTSHeadline(op, types[Text].Usage(), usages)
			if err != nil {
				t.Fatal(op, err)
			}
			if err := plan.ValidateValues(t.Context(), values); err != nil {
				t.Fatal(err)
			}
			for _, collation := range []uint32{950, 951} {
				result := types[Text].Usage()
				result.Collation.ID = collation
				collated, err := BindFTSHeadline(op, result, usages)
				if err != nil || collated != plan {
					t.Fatal("collation changed descriptor-backed plan", collation, err)
				}
				if err := collated.ValidateValues(t.Context(), values); err != nil {
					t.Fatal("collated values", err)
				}
				for i := range op.Arity() {
					nulls := values
					nulls[i] = Null(usages[i].Type)
					if err := collated.ValidateValues(t.Context(), nulls); err != nil {
						t.Fatal("collated typed NULL", i, err)
					}
					nulls[i] = Null(foreign[op.Formal(i)])
					if err := collated.ValidateValues(t.Context(), nulls); err == nil {
						t.Fatal("collated foreign NULL", i)
					}
				}
				badResults := []Usage{{}, foreign[Text].Usage(), types[VarChar].Usage(), result, result}
				badResults[1].Collation.ID = collation
				badResults[3].Typmod = 0
				badResults[4].Collation.ID = 999999
				for _, bad := range badResults {
					if _, err := BindFTSHeadline(op, bad, usages); err == nil {
						t.Fatal("invalid collated result admitted", bad)
					}
				}
				foreignResult := collated
				foreignResult.result = foreign[Text]
				nulls := values
				for i := range op.Arity() {
					nulls[i] = Null(usages[i].Type)
				}
				if err := foreignResult.ValidateValues(t.Context(), nulls); err == nil {
					t.Fatal("all NULL inputs concealed foreign result generation")
				}
			}
			for i := range op.Arity() {
				nulls := values
				nulls[i] = Null(usages[i].Type)
				if err := plan.ValidateValues(t.Context(), nulls); err != nil {
					t.Fatal("typed NULL", err)
				}
				bad := values
				bad[i] = Null(foreign[op.Formal(i)])
				if err := plan.ValidateValues(t.Context(), bad); err == nil {
					t.Fatal("foreign NULL", i)
				}
				badUsage := usages
				badUsage[i] = foreign[op.Formal(i)].Usage()
				if _, err := BindFTSHeadline(op, types[Text].Usage(), badUsage); err == nil {
					t.Fatal("foreign input", i)
				}
			}
			if _, err := BindFTSHeadline(op, foreign[Text].Usage(), usages); err == nil {
				t.Fatal("foreign result")
			}
			qindex := 1
			if explicit {
				qindex = 2
			}
			bad := values
			bad[qindex] = Value{typ: types[TSQuery]}
			if err := plan.ValidateValues(t.Context(), bad); err == nil {
				t.Fatal("invalid query payload")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := plan.ValidateValues(ctx, values); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
	var builder fts.HeadlineBuilder
	builder.SetTokens([]fts.DocumentToken{{ID: 1, Text: "Cat"}})
	if err := builder.Associate(t.Context(), 0, 1, fts.LexicalPosition{Position: 1, Lexemes: []fts.Lexeme{{Text: "cat"}}}); err != nil {
		t.Fatal(err)
	}
	document, err := builder.Finish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	query, err := types[TSQuery].Input(t.Context(), "cat", -1)
	if err != nil {
		t.Fatal(err)
	}
	value, err := FTSHeadlineResult(t.Context(), types[Text], document, query, String(types[Text], "HighlightAll=true"))
	if err != nil || value.typ != types[Text] || value.text != "<b>Cat</b>" {
		t.Fatal(value, err)
	}
	for _, tc := range []struct {
		result         *Descriptor
		query, options Value
	}{
		{foreign[Text], query, String(types[Text], "")},
		{types[Text], Null(foreign[TSQuery]), String(types[Text], "")},
		{types[Text], query, Null(foreign[Text])},
	} {
		if _, err := FTSHeadlineResult(t.Context(), tc.result, document, tc.query, tc.options); err == nil {
			t.Fatal("runtime accepted foreign authority")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := FTSHeadlineResult(ctx, types[Text], document, query, String(types[Text], "")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
