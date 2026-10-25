package schemacmds_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)


type citextRegexpOrderProbe struct {
	plan.LiteralExpr
	order *[]int
	slot  int
}

func (p citextRegexpOrderProbe) Eval(ctx expr.Context) (sqltype.Value, error) {
	*p.order = append(*p.order, p.slot)
	return p.LiteralExpr.Eval(ctx)
}

func citextRegexpWrittenOrder(t *testing.T) {
	t.Helper()
	e, installation, base := citextUnitInstalled(t)
	call := citextRegexpUnitCall(t, e.CatalogRead(), installation, base, 1)
	original := call.Args[0].(plan.LiteralExpr).Value
	var order []int
	args := make([]expr.Expr, len(call.Args))
	for i, argument := range call.Args {
		args[i] = citextRegexpOrderProbe{argument.(plan.LiteralExpr), &order, i}
	}
	call.WrittenSlots = [7]uint8{2, 0, 1}
	call.Labels = [7]string{"flags", "string", "pattern"}
	bound, err := call.Reconstruct(call.Info, args, call.Plan.Collation(), call.Plan.Conflict())
	if err != nil {
		t.Fatal(err)
	}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	value, err := (functions.Regexp{RegexpCall: bound}).Eval(ctx)
	if err != nil || !slices.Equal(order, []int{2, 0, 1}) {
		t.Fatal("named arguments lost written evaluation order", order, value, err)
	}
	if text, err := value.Type().Output(context.Background(), value); err != nil || text != "{A}" {
		t.Fatal("original capture", text, err)
	}
	if call.Args[0].(plan.LiteralExpr).Value != original {
		t.Fatal("borrowed source changed")
	}
	var values [7]sqltype.Value
	for i, argument := range call.Args {
		values[i] = argument.(plan.LiteralExpr).Value
	}
	state := new(sqltype.PGRegexExecution)
	values[1] = sqltype.String(base.SQLType, "[")
	if _, err := call.Plan.Evaluate(context.Background(), values, state); err == nil {
		t.Fatal("invalid pattern accepted")
	}
	values[1] = sqltype.Null(base.SQLType)
	if value, err := call.Plan.Evaluate(context.Background(), values, state); err != nil || !value.IsNull() {
		t.Fatal("NULL after failure", value, err)
	}
	values[1] = sqltype.String(base.SQLType, "a")
	values[2] = sqltype.String(call.Citext.Text.Type, "c")
	if value, err := call.Plan.Evaluate(context.Background(), values, state); err != nil || !value.IsNull() {
		t.Fatal("sensitive flags after failure", value, err)
	}
	values[2] = sqltype.String(call.Citext.Text.Type, "i")
	value, err = call.Plan.Evaluate(context.Background(), values, state)
	if err != nil {
		t.Fatal("valid recovery", err)
	}
	if text, err := value.Type().Output(context.Background(), value); err != nil || text != "{A}" {
		t.Fatal("stale program after recovery", text, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = &exec.Context{EvalStatement: exec.EvalStatement{Context: canceled}}
	if _, err := (functions.Regexp{RegexpCall: bound}).Eval(ctx); !errors.Is(err, context.Canceled) || len(order) != 3 {
		t.Fatal("cancellation evaluated arguments", order, err)
	}
}

func citextRegexpSQLBodyBoundaries(t *testing.T) {
	t.Helper()
	e, installation, base := citextUnitInstalled(t)
	call := citextRegexpUnitCall(t, e.CatalogRead(), installation, base, 0)
	if (functions.Regexp{RegexpCall: call}).PreflightFold() != expr.PreflightRegexNullOrEvaluate ||
		(functions.Regexp{}).PreflightFold() != expr.PreflightEvaluate {
		t.Fatal("captured scalar lost the shared typed-NULL pruning frontier")
	}
	set := citextRegexpUnitCall(t, e.CatalogRead(), installation, base, 2)
	if (functions.RegexpSet{RegexpCall: set}).PreflightFold() != expr.PreflightChildren {
		t.Fatal("strict SRF initialization was incorrectly pruned")
	}
	// Body collation is independent of the simplifier's invocation provenance.
	for _, inline := range []bool{false, true} {
		scoped := call
		capture := *call.Citext
		capture.Inline = inline
		scoped.Citext = &capture
		bound, err := scoped.Reconstruct(scoped.Info, scoped.Args, scoped.Plan.Collation(), true)
		if err != nil || bound.Plan.Conflict() || bound.Plan.Collation() != call.Citext.Text.Collation {
			t.Fatal("SQL body inherited the outer call's indeterminate collation", inline, err)
		}
	}
	args := append([]expr.Expr(nil), call.Args...)
	args[1] = plan.LiteralExpr{Info: expr.Info{Usage: base.SQLType.Usage()}, Value: sqltype.String(base.SQLType, "[")}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.CatalogRead()}}
	for _, inline := range []bool{false, true} {
		scoped := call
		capture := *call.Citext
		capture.Inline = inline
		scoped.Citext = &capture
		scoped, err := scoped.Reconstruct(scoped.Info, args, scoped.Plan.Collation(), false)
		if err != nil { t.Fatal(err) }
		_, err = (functions.Regexp{RegexpCall: scoped}).Eval(ctx)
		var diagnostic *sqlstate.Error
		want := `SQL function "regexp_match" statement 1`
		if inline { want = "" }
		if !errors.As(err, &diagnostic) || diagnostic.Code != "2201B" ||
			diagnostic.Message != "invalid regular expression: brackets [] not balanced" || diagnostic.Context != want {
			t.Fatal("SQL invocation versus scalar-inline provenance", inline, err)
		}
	}
	rowCall := functions.Regexp{RegexpCall: call}
	rowCall.Property.RowDependent = true
	next, changed, err := exec.PreflightCoercions(ctx, rowCall)
	if err != nil || !changed || !next.(functions.Regexp).Citext.Inline || call.Citext.Inline {
		t.Fatal("nonconstant scalar simplification lost copy-on-write provenance", next, err)
	}
	flags := functions.Regexp{RegexpCall: citextRegexpUnitCall(t, e.CatalogRead(), installation, base, 1)}
	flags.Property.RowDependent = true
	next, _, err = exec.PreflightCoercions(ctx, flags)
	if err != nil || next.(functions.Regexp).Citext.Inline {
		t.Fatal("flags wrapper was incorrectly inlined", next, err)
	}
	for _, index := range []int{4, 5} {
		replace := citextRegexpUnitCall(t, e.CatalogRead(), installation, base, index)
		var values [7]sqltype.Value
		for i, argument := range replace.Args {
			values[i] = argument.(plan.LiteralExpr).Value
		}
		if index == 4 {
			values[1] = sqltype.String(base.SQLType, "z")
		} else {
			values[3] = sqltype.String(replace.Citext.Text.Type, "c")
		}
		original := values[0]
		result, err := replace.Plan.Evaluate(context.Background(), values, new(sqltype.PGRegexExecution))
		if err != nil || result.Type() != replace.Info.Usage.Type || result.Type().ID() != sqltype.Text {
			t.Fatal("unchanged replacement retained a citext result descriptor", result, err)
		}
		text, err := result.Type().Output(context.Background(), result)
		if err != nil || text != "AbA" || values[0] != original || values[0].Type() != base.SQLType {
			t.Fatal("unchanged replacement changed source spelling or authority", text, err)
		}
	}
}
