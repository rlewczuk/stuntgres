package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// These units exercise retained package plans, not public SQL execution.
func TestStringCaseRetainedPlan(t *testing.T) {
	var text, replacement *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
		}
	}
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			replacement = d
		}
	}
	for _, conflict := range []bool{false, true} {
		usage := text.Usage()
		usage.Collation.ID = 6411
		plan, err := sqltype.BindString("upper", usage, text.Usage(), []sqltype.Usage{usage}, [4]bool{true}, conflict)
		if err != nil {
			t.Fatal(err)
		}
		node := stringExpression{
			expressionInfo: expressionInfo{usage: usage, collationConflict: conflict},
			plan:           plan, textual: [4]bool{true}, syntax: parser.OrdinaryStringSyntax,
			args: []BoundExpr{literalExpression{expressionInfo{usage: usage}, sqltype.String(text, "ß")}},
		}
		rebound, changed, err := retagBoundTypes(New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			if d == text {
				return replacement
			}
			return d
		})
		if err != nil || !changed {
			t.Fatal("retag", changed, err)
		}
		retained := rebound.(stringExpression)
		if retained.plan.CollationIndeterminate() != conflict || retained.CollationIndeterminate() != conflict || retained.ResultUsage().Collation.ID != 6411 || retained.plan.TextUsage().Type != replacement {
			t.Fatal("retag lost authority", retained)
		}
		if node.plan.TextUsage().Type != text || node.args[0].ResultUsage().Type != text {
			t.Fatal("retag mutated source")
		}
		got, err := retained.Eval(&EvalContext{Context: context.Background()})
		if conflict {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" || diagnostic.Message != "could not determine which collation to use for upper() function" {
				t.Fatal("retained conflict", err)
			}
		} else {
			s, textErr := got.Text()
			if err != nil || textErr != nil || s != "SS" || got.Type() != replacement {
				t.Fatal("retained Unicode expansion", s, err, textErr)
			}
		}
	}
}

func TestStringCaseOnceOnlyChildAndMetadata(t *testing.T) {
	var text *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
		}
	}
	usage := text.Usage()
	usage.Collation.ID = 811
	plan, err := sqltype.BindString("lower", usage, text.Usage(), []sqltype.Usage{usage}, [4]bool{true}, false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	node := stringExpression{
		expressionInfo: expressionInfo{usage: usage, properties: ExprProperties{Volatility: Immutable, RowDependent: true}},
		plan:           plan, textual: [4]bool{true}, syntax: parser.OrdinaryStringSyntax,
		args: []BoundExpr{mathObservedArgument{expressionInfo: expressionInfo{usage: usage}, value: sqltype.String(text, "İ"), order: &order, index: 0}},
	}
	got, err := node.Eval(&EvalContext{Context: context.Background()})
	s, textErr := got.Text()
	if err != nil || textErr != nil || s != "i" || !reflect.DeepEqual(order, []int{0}) || node.ResultUsage() != usage || node.ResultUsage().Typmod != -1 {
		t.Fatal("once-only selected case", s, order, err, textErr)
	}
	info, err := collatedInfo(New().CatalogRead(), expressionInfo{usage: usage}, node)
	if err != nil || info.usage.Collation.ID != 811 || info.collationConflict {
		t.Fatal("nested authority", info, err)
	}
}

func TestStringCaseCollatedParameterInference(t *testing.T) {
	for _, name := range []string{"lower", "upper", "casefold", "initcap"} {
		t.Run(name, func(t *testing.T) {
			e := New()
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			e.mu.Lock()
			defer e.mu.Unlock()
			text := b.typ(sqltype.Text)
			for _, collation := range []struct {
				name string
				id   uint32
			}{{"pg_c_utf8", 811}, {"pg_unicode_fast", 6411}} {
				raw := parser.FunctionExpr{
					Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}},
					Args: []parser.Expr{parser.CollateExpr{
						Child: parser.ParameterExpr{Number: 1, Pos: parser.Position{Character: 14}},
						Name:  parser.ObjectName{Parts: []parser.Identifier{{Text: collation.name}}},
						Pos:   parser.Position{Character: 17},
					}},
				}
				node, err := b.bindStringFunction(raw, true)
				if err != nil {
					t.Fatal(err)
				}
				bound := node.(stringExpression)
				input := bound.args[0].(collationExpression)
				if b.parameters[1] != text.Usage() || input.child.ResultUsage() != text.Usage() ||
					input.ResultUsage().Collation.ID != collation.id || bound.ResultUsage().Collation.ID != collation.id ||
					!bound.Properties().ParameterDependent {
					t.Fatal("lost parameter type or occurrence collation", b.parameters, input, bound)
				}
			}
		})
	}
}

func TestStringCaseCollatedTypedParameterDiagnostic(t *testing.T) {
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	expected := &StatementDescription{Parameters: ParameterInfo{Count: 1, Types: []sqltype.Usage{integer.Usage()}}}
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
	established := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, b.op, nil)
	established.parameters[1] = integer.Usage()
	e.mu.Lock()
	defer e.mu.Unlock()
	raw := parser.FunctionExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "lower"}}},
		Args: []parser.Expr{parser.CollateExpr{
			Child: parser.ParameterExpr{Number: 1},
			Name:  parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_c_utf8"}}},
			Pos:   parser.Position{Character: 17},
		}},
	}
	for _, binder := range []*expressionBinder{b, established} {
		node, err := binder.bindStringFunction(raw, true)
		var diagnostic *sqlstate.Error
		if node != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42804" ||
			diagnostic.Message != "collations are not supported by type integer" || diagnostic.Position != "17" {
			t.Fatal("typed parameter was inferred as TEXT", node, err)
		}
	}
}
