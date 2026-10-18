package engine

import (
	"stuntgres/internal/engine/functions"
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"unicode/utf8"
)

func TestJSONObjectAggregateModifierProviderCompletion(t *testing.T) {
	testJSONObjectAggregateModifierProviderCompletion(t, []string{"json_object_agg", "jsonb_object_agg", "json_object_agg_strict", "jsonb_object_agg_strict"})
}

func TestJSONObjectUniqueAggregateModifierProviderCompletion(t *testing.T) {
	testJSONObjectAggregateModifierProviderCompletion(t, []string{"json_object_agg_unique", "jsonb_object_agg_unique", "json_object_agg_unique_strict", "jsonb_object_agg_unique_strict"})
}

func testJSONObjectAggregateModifierProviderCompletion(t *testing.T, names []string) {
	t.Helper()
	for _, name := range names {
		for _, modifier := range []string{"distinct", "order", "distinct-order"} {
			for _, slot := range []string{"key", "value", "both", "typed-first"} {
				t.Run(name+"/"+modifier+"/"+slot, func(t *testing.T) {
					e := New()
					ctx := context.Background()
					b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
					b.aggregateClause = "SELECT"
					text, _ := e.CatalogRead().LookupType(sqltype.Text)
					integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
					args, order := "$1,1", "$1"
					if slot == "value" || slot == "typed-first" {
						args = "'a',$1"
					}
					if slot == "both" {
						args, order = "$1,$2", "$1,$2"
					}
					if slot == "typed-first" {
						b.parameters[1] = integer.Usage()
					}
					if modifier != "order" {
						args = "DISTINCT " + args
					}
					if modifier != "distinct" {
						args += " ORDER BY " + order
					}
					raw := statement(t, "/*é😀*/ SELECT "+name+"("+args+")").(*parser.Select).Targets[0]
					node, err := b.bind(raw, expressionPolicy{context: projectionContext})
					if err != nil {
						t.Fatal(err)
					}
					parameters, err := b.finish()
					if err != nil {
						t.Fatal("completed input retained unresolved occurrence", err)
					}
					wantCount := 1
					if slot == "both" {
						wantCount = 2
					}
					if parameters.Count != wantCount || len(parameters.Types) != wantCount {
						t.Fatal("incorrect completed parameter count", parameters)
					}
					wantType := text
					if slot == "typed-first" {
						wantType = integer
					}
					if parameters.Types[0].Type != wantType || len(b.anyParameters) != 0 {
						t.Fatal("incorrect parameter completion", parameters, b.anyParameters)
					}
					call := b.aggregates[node.(aggregateExpression).slot]
					provider := call.provider.(functions.JSONObjectAggregate)
					if provider.KeyUsage() != call.argument.ResultUsage() || provider.InputUsage() != call.separator.ResultUsage() {
						t.Fatal("provider captured provisional inputs")
					}
					if provider.Unique() != strings.Contains(name, "_unique") || provider.Strict() != strings.HasSuffix(name, "_strict") {
						t.Fatal("modifier completion lost object policy", provider)
					}
					key, input := sqltype.String(text, "x"), sqltype.String(text, "one")
					want := `{ "x" : 1 }`
					if slot == "key" {
						input, _ = sqltype.Integer(integer, 1)
					}
					if slot == "value" {
						key = sqltype.String(text, "a")
						want = `{ "a" : "one" }`
					}
					if slot == "both" {
						want = `{ "x" : "one" }`
					}
					if slot == "typed-first" {
						key = sqltype.String(text, "a")
						input, _ = sqltype.Integer(integer, 1)
						want = `{ "a" : 1 }`
					}
					if strings.HasPrefix(name, "jsonb_") {
						switch slot {
						case "key":
							want = `{"x": 1}`
						case "value":
							want = `{"a": "one"}`
						case "both":
							want = `{"x": "one"}`
						case "typed-first":
							want = `{"a": 1}`
						}
					}
					state, err := provider.Init(ctx)
					if err != nil {
						t.Fatal("provider plan not rebound", err)
					}
					if err = provider.Advance(ctx, state, key, input); err != nil {
						t.Fatal("provider uses stale owners", err)
					}
					value, err := provider.Finalize(ctx, state)
					if err != nil {
						t.Fatal(err)
					}
					got, err := value.Type().Output(ctx, value)
					if err != nil || got != want {
						t.Fatal("completed provider output", got, want, err)
					}
					if len(call.order) > 0 {
						wantSlot := 0
						if slot == "value" || slot == "typed-first" {
							wantSlot = 1
						}
						if call.order[0].slot != wantSlot {
							t.Fatal("matching order lost input slot", call.order[0].slot, wantSlot)
						}
					}
					if slot == "both" && parameters.Types[1].Type != text {
						t.Fatal("second parameter remains untyped")
					}
				})
			}
		}
	}
}

func TestJSONObjectAggregateModifierOccurrenceRetention(t *testing.T) {
	testJSONObjectAggregateModifierOccurrenceRetention(t, "json_object_agg")
}

func TestJSONObjectUniqueAggregateModifierOccurrenceRetention(t *testing.T) {
	for _, name := range []string{"json_object_agg_unique", "jsonb_object_agg_unique", "json_object_agg_unique_strict", "jsonb_object_agg_unique_strict"} {
		testJSONObjectAggregateModifierOccurrenceRetention(t, name)
	}
}

func testJSONObjectAggregateModifierOccurrenceRetention(t *testing.T, name string) {
	t.Helper()
	for _, tc := range []struct{ q, token string }{
		{"/*é😀*/ SELECT json_object_agg($1,1) AS earlier,json_object_agg('a',$1 ORDER BY $1)", "$1,1"},
		{"/*é😀*/ SELECT json_object_agg('a',$1) AS earlier,json_object_agg(DISTINCT 'a',$1)", "$1) AS earlier"},
		{"/*é😀*/ SELECT json_object_agg($1,$1 ORDER BY $1)", "$1 ORDER"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			tc.q = strings.ReplaceAll(tc.q, "json_object_agg", name)
			e := New()
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			b.aggregateClause = "SELECT"
			raw := statement(t, tc.q).(*parser.Select)
			for _, target := range raw.Targets {
				if _, err := b.bind(target, expressionPolicy{context: projectionContext}); err != nil {
					t.Fatal("modifier analysis failed before occurrence validation", err)
				}
			}
			_, err := b.finish()
			var d *sqlstate.Error
			// The token is an actual source occurrence, not just the shared number.
			position := utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1
			if !errors.As(err, &d) || d.Code != "42P08" || d.Message != "could not determine data type of parameter $1" || d.Position != strconv.Itoa(position) || len(b.anyParameters) != 1 {
				t.Fatal("unrelated occurrence retired", b.anyParameters, err)
			}
		})
	}
}

func TestJSONObjectAggregateModifierRepeatedDistinctCompletion(t *testing.T) {
	for _, order := range []string{"", " ORDER BY $1"} {
		e := New()
		ctx := context.Background()
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		b.aggregateClause = "SELECT"
		raw := statement(t, "/*é😀*/ SELECT json_object_agg(DISTINCT $1,$1"+order+")").(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = b.finish(); err != nil || len(b.anyParameters) != 0 {
			t.Fatal("DISTINCT did not complete both occurrences", b.anyParameters, err)
		}
		call := b.aggregates[node.(aggregateExpression).slot]
		p := call.provider.(functions.JSONObjectAggregate)
		text, _ := e.CatalogRead().LookupType(sqltype.Text)
		if p.KeyUsage().Type != text || p.InputUsage().Type != text || len(call.distinct) != 2 {
			t.Fatal("same-number inputs lost distinct tuple", p.KeyUsage(), p.InputUsage(), len(call.distinct))
		}
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		value := sqltype.String(text, "x")
		if err = p.Advance(ctx, state, value, value); err != nil {
			t.Fatal(err)
		}
		out, err := p.Finalize(ctx, state)
		if err != nil {
			t.Fatal(err)
		}
		got, err := out.Type().Output(ctx, out)
		if err != nil || got != `{ "x" : "x" }` {
			t.Fatal("repeated completed input output", got, err)
		}
		if len(call.order) > 0 && call.order[0].slot != 0 {
			t.Fatal("ORDER did not keep first equal target")
		}
	}
}
