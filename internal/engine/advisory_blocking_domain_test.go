package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestAdvisoryBlockingDomainInput(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, ddl := range []string{
		"CREATE DOMAIN public.advisory_inner AS integer CHECK(VALUE > 0)",
		"CREATE DOMAIN public.advisory_outer AS integer CHECK(VALUE >= 0)",
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, ddl))
	}
	view := e.CatalogRead()
	outer := domainUnitType(t, view, "advisory_outer")
	target := outer.SQLType
	integer, _ := view.LookupType(sqltype.Int4)
	c := &EvalContext{evalStatement: evalStatement{Context: context.Background(), catalog: view}}
	// Volatile calls are not admitted in stored domain declarations. Exercise
	// the real active-frame executor instead, replacing only this context's
	// detached executable recipe after ordinary programme initialization.
	// Nothing is published or rendered as a supported SQL domain definition.
	if err := c.prepareDomainChecks(outer); err != nil {
		t.Fatal(err)
	}
	probeBinder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	base := target.DomainBase()
	probeBinder.domainValueUsage = &base
	probeBinder.consumer.checkConsumer, probeBinder.params.noParameters = true, true
	probeBinder.queryNamespace = &queryNamespace{}
	probeRaw := statement(t, "SELECT pg_try_advisory_lock(NULL::integer, (VALUE::public.advisory_inner)::integer) IS NULL AND pg_blocking_pids(NULLIF((VALUE::public.advisory_inner)::integer,(VALUE::public.advisory_inner)::integer)) IS NULL AND CASE WHEN false THEN pg_safe_snapshot_blocking_pids(1/0) IS NULL ELSE true END").(*parser.Select).Targets[0]
	probe, err := probeBinder.bindBoolean(probeRaw, projectionContext, "CHECK")
	if err != nil {
		t.Fatal(err)
	}
	programme := c.preparedDomainChecks[outer]
	programme.expressions = []expr.Expr{probe}
	c.preparedDomainChecks[outer] = programme
	for _, number := range []int64{1, 0} {
		value, err := sqltype.Integer(integer, number)
		if err != nil {
			t.Fatal(err)
		}
		err = c.CheckDomain(c.Context, target, value)
		if number == 1 && err != nil {
			t.Fatal("active outer frame failed nested conversion", err)
		}
		if number == 0 {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" || diagnostic.DataType != "advisory_inner" {
				t.Fatal("NULL suppressed demanded nested conversion", err)
			}
		}
		if c.domainValueActive || c.Input.DomainChecks != nil || c.domainFrameUsage.Type != nil || c.domainValue.Type() != nil {
			t.Fatal("domain frame/input escaped", c.domainFrameUsage, c.Input.DomainChecks)
		}
	}
	if err := c.CheckDomain(c.Context, target, sqltype.Null(integer)); err != nil {
		t.Fatal("NULL candidate", err)
	}
	if c.domainValueActive || c.Input.DomainChecks != nil {
		t.Fatal("NULL frame escaped")
	}

	// The old fixed advisory cast keeps child properties and does not add a
	// cast routine reference. Compare that behavior at the real Binder port.
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := statement(t, "SELECT pg_advisory_lock(1::smallint)").(*parser.Select).Targets[0].(parser.FunctionExpr)
	argument, err := b.AnalyzeArgument(raw.Args[0], functions.AnalysisRequest{Intent: functions.DirectDeferredArgument})
	if err != nil {
		t.Fatal(err)
	}
	node, err := b.bindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	cast := node.(functions.Advisory).Args[0].(coercionExpression)
	if cast.Properties() != argument.Expression.Properties() || expr.InfoOf(cast).RoutineReference != nil {
		t.Fatal("bare advisory coercion changed properties or routine identity", cast.Properties(), argument.Expression.Properties())
	}
	// Reconstruct an actual CASE child through the generic simplifier. The
	// inquiry follows the resulting child's properties, not stale outer facts.
	caseRaw := statement(t, "SELECT pg_blocking_pids(CASE WHEN true THEN 0 ELSE $1 END)").(*parser.Select).Targets[0]
	boundCase, err := b.bind(caseRaw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	original := boundCase.(functions.BlockingInquiry)
	original.Property.RowDependent = true
	simplified, _, err := simplifyConditional(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, original, false)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := simplified.(functions.BlockingInquiry)
	want := rebuilt.Arg.Properties()
	want.Volatility = expr.Volatile
	if rebuilt.Properties() != want || rebuilt.Properties().RowDependent || !original.Properties().RowDependent {
		t.Fatal("CASE reconstruction changed property rule or source", rebuilt.Properties(), want, original.Properties())
	}
	staged := expr.InfoOf(original)
	staged.Property.StageDependent = true
	substitute, err := expr.Substitute(original, staged, []expr.Expr{rebuilt.Arg})
	if err != nil || substitute.Properties() != staged.Property || substitute.ResultUsage() != staged.Usage || substitute.(functions.BlockingInquiry).Kind != original.Kind {
		t.Fatal("stage substitution lost supplied facts", substitute, err)
	}
}
