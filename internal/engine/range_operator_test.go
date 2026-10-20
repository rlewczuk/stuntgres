package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

type rangeOperatorProbe struct {
	expr.Info
	value   sqltype.Value
	calls   *int
	failure error
}

func (e rangeOperatorProbe) Eval(expr.Context) (sqltype.Value, error) {
	*e.calls++
	return e.value, e.failure
}

func TestRangeOperatorCarrierEvaluation(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	d := b.typ(sqltype.Int4Range)
	v, err := d.Input(ctx, "[1,3)", -1)
	if err != nil {
		t.Fatal(err)
	}
	counts := [2]int{}
	args := [2]expr.Expr{rangeOperatorProbe{b.info(d.Usage(), parser.Position{}, expr.Properties{RowDependent: true}), v, &counts[0], nil}, rangeOperatorProbe{b.info(d.Usage(), parser.Position{}, expr.Properties{ParameterDependent: true}), v, &counts[1], nil}}
	info := b.info(b.typ(sqltype.Bool).Usage(), parser.Position{}, expr.MergeProperties(args[0].Properties(), args[1].Properties()))
	e, err := functions.NewRangeOperator(info, sqltype.RangeContains, args, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Eval(&EvalContext{EvalStatement: evalStatement{Context: ctx}})
	if err != nil || got.IsNull() || counts != [2]int{1, 1} {
		t.Fatal(got, counts, err)
	}
	props := e.Properties()
	if !props.RowDependent || !props.ParameterDependent {
		t.Fatal("lost operand properties")
	}
	failure := errors.New("child failure")
	args[0] = rangeOperatorProbe{b.info(d.Usage(), parser.Position{}, expr.Properties{}), v, &counts[0], failure}
	counts = [2]int{}
	e, err = e.Reconstruct(info, args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Eval(&EvalContext{EvalStatement: evalStatement{Context: ctx}}); !errors.Is(err, failure) || counts != [2]int{1, 0} {
		t.Fatal("child demand", counts, err)
	}
	args[1] = plan.LiteralExpr{Info: b.info(d.Usage(), parser.Position{}, expr.Properties{}), Value: sqltype.Null(d)}
	counts = [2]int{}
	e, err = e.Reconstruct(info, args)
	if err != nil {
		t.Fatal(err)
	}
	got, err = e.Eval(&EvalContext{EvalStatement: evalStatement{Context: ctx}})
	if err != nil || !got.IsNull() || counts != [2]int{} {
		t.Fatal("literal NULL demanded child", got, counts, err)
	}
}
func TestRangeOperatorRetagAndStoredAuthority(t *testing.T) {
	ctx := context.Background()
	source, target := New(), New()
	b := newTestExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	fresh := newTestExpressionBinder(t, ctx, target, sqltype.InputEnvironment{}, nil)
	d := b.typ(sqltype.NumRange)
	v, err := d.Input(ctx, "[1.0,2.00]", -1)
	if err != nil {
		t.Fatal(err)
	}
	arg := plan.LiteralExpr{Info: b.info(d.Usage(), parser.Position{}, expr.Properties{Volatility: expr.Immutable}), Value: v}
	original, err := functions.NewRangeOperator(b.info(d.Usage(), parser.Position{Character: 8}, expr.Properties{Volatility: expr.Immutable}), sqltype.RangeUnion, [2]expr.Expr{arg, arg}, "pg_catalog")
	if err != nil {
		t.Fatal(err)
	}
	mapped, changed, err := retagBoundTypes(target.CatalogRead(), original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh.typ(d.ID()) })
	if err != nil || !changed {
		t.Fatal(mapped, changed, err)
	}
	next := mapped.(functions.RangeOperator)
	value, err := next.Eval(&EvalContext{EvalStatement: evalStatement{Context: ctx}})
	if err != nil || value.Type() != fresh.typ(sqltype.NumRange) {
		t.Fatal(value, err)
	}
	if original.Args[0].ResultUsage().Type != d || next.Namespace != "pg_catalog" {
		t.Fatal("mutated retained identity")
	}
	if !sameGroupExpression(original, original) || sameGroupExpression(original, next) {
		t.Fatal("wrong expression equivalence")
	}
	if !generationImmutable(next) || exec.PlannedVolatility(next) != expr.Immutable || !conditionalConstantInputs(next) {
		t.Fatal("immutable carrier properties")
	}
	folded, did, err := preflightCoercions(&EvalContext{EvalStatement: evalStatement{Context: ctx}}, next)
	if err != nil || !did {
		t.Fatal(folded, did, err)
	}
	value, err = folded.Eval(&EvalContext{EvalStatement: evalStatement{Context: ctx}})
	if err != nil || value.Type() != fresh.typ(sqltype.NumRange) {
		t.Fatal("folded generation", value, err)
	}
	forged := next
	forged.Plan = original.Plan
	if err := forged.Validate(); err == nil {
		t.Fatal("stale provider survived validation")
	}
	text, err := (scalarRenderer{}).scalarFunctionSQL(ctx, target.CatalogRead(), next)
	if err != nil || !strings.Contains(text, `OPERATOR("pg_catalog".+)`) {
		t.Fatal("namespace spelling", text, err)
	}
}
