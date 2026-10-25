package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDatetimeArithmeticBindingAndPhysicalVolatility(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		expression string
		result     sqltype.ID
		volatility expr.Volatility
	}{
		{"DATE '2024-01-31'+INTERVAL '1 month'", sqltype.Timestamp, expr.Immutable},
		{"DATE '2024-01-02'-DATE '2024-01-01'", sqltype.Int4, expr.Immutable},
		{"TIMESTAMPTZ '2005-04-02 12:00-07'+INTERVAL '0'", sqltype.Timestamptz, expr.Stable},
		{"TIMESTAMPTZ '2005-04-02 12:00-07'-INTERVAL '1 microsecond'", sqltype.Timestamptz, expr.Stable},
		{"INTERVAL '1 month'+TIMESTAMPTZ '2005-04-02 12:00-07'", sqltype.Timestamptz, expr.Stable},
		{"TIMESTAMPTZ '2005-04-02 12:00-07'-TIMESTAMPTZ '2005-04-01 12:00-07'", sqltype.Interval, expr.Immutable},
		{"TIME '12:00'=TIMETZ '12:00+00'", sqltype.Bool, expr.Stable},
		{"TIMETZ '12:00+00'=TIME '12:00'", sqltype.Bool, expr.Stable},
	} {
		e := engine.New()
		b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, "SELECT "+tc.expression+" FROM t").(*parser.Select).Targets[0]
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(tc, err)
		}
		if node.ResultUsage().Type != b.Type(tc.result) || node.ResultUsage().Typmod != -1 || node.Properties().Volatility != tc.volatility || exec.PlannedVolatility(node) != tc.volatility || plan.GenerationImmutable(node) != (tc.volatility == expr.Immutable) {
			t.Fatal(tc, node, node.Properties())
		}
		folded, changed, err := exec.SimplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, node, false)
		if err != nil {
			t.Fatal(err)
		}
		_, literal := folded.(plan.LiteralExpr)
		if tc.volatility == expr.Stable && (changed || literal) {
			t.Fatal("stable literal arithmetic folded", tc, folded)
		}
		if tc.volatility == expr.Immutable && !literal {
			t.Fatal("immutable operation did not fold", tc, folded)
		}
	}
}

func TestDatetimeArithmeticDependenceAndVolatileDominance(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, expression := range []string{"$1::TIMESTAMPTZ+INTERVAL '0'", "INTERVAL '0'+$1::TIMESTAMPTZ"} {
		node, err := b.Bind(enginetest.Statement(t, "SELECT "+expression+" FROM t").(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil || !node.Properties().ParameterDependent || node.Properties().Volatility != expr.Stable {
			t.Fatal(node, err)
		}
		n := node.(plan.ArithmeticExpr)
		child := plan.ParameterExpr{Info: expr.Info{Usage: b.Type(sqltype.Timestamptz).Usage(), Property: expr.Properties{Volatility: expr.Volatile, RowDependent: true, ParameterDependent: true, StageDependent: true}}, Number: 1}
		if n.Left.ResultUsage().Type.ID() == sqltype.Timestamptz {
			n.Left = child
		} else {
			n.Right = child
		}
		if exec.PlannedVolatility(n) != expr.Volatile || plan.GenerationImmutable(n) {
			t.Fatal("intrinsic stable suppressed child", n)
		}
	}
}

func TestDatetimeArithmeticDefaultRegistryRebind(t *testing.T) {
	ctx := context.Background()
	source, target := engine.New(), engine.New()
	b := enginetest.ExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	b.Consumer.DefaultConsumer = true
	node, err := b.Bind(enginetest.Statement(t, "SELECT TIMESTAMPTZ '2005-04-02 12:00-07'+INTERVAL '1 day' FROM t").(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	// Rebinding must own the target registry result, not a source operand descriptor.
	rebound, changed, err := stored.RetagTypes(target.CatalogRead(), node, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		next, _ := target.CatalogRead().LookupType(old.ID())
		return next
	})
	if err != nil || !changed {
		t.Fatal(rebound, changed, err)
	}
	mapper := stored.ViewQueryRemapper{Catalog: target.CatalogRead(), Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: replacementProcedurePolicyFixture(source, target)}
	query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{rebound}}})
	if err != nil {
		t.Fatal(err)
	}
	rebound = query.(*plan.ValuesNode).Rows[0][0]
	n := rebound.(plan.ArithmeticExpr)
	typ, _ := target.CatalogRead().LookupType(sqltype.Timestamptz)
	if n.ResultUsage().Type != typ || n.Provider.ResultType() != typ || !n.Provider.SessionDependent() || exec.PlannedVolatility(n) != expr.Stable {
		t.Fatal(n)
	}
	text, err := stored.DefaultSQL(ctx, target.CatalogRead(), rebound)
	if err != nil || text == "" {
		t.Fatal("symbolic target default", text, err)
	}
	original, err := stored.DefaultSQL(ctx, source.CatalogRead(), node)
	if err != nil || original != text {
		t.Fatal("rebind changed symbolic meaning", original, text, err)
	}
	if node.ResultUsage().Type == typ {
		t.Fatal("source registry ownership mutated")
	}
}

// replacementProcedurePolicyFixture supplies the real host policy for private
// address/type remap fixtures; it does not manufacture numeric principal links.
func replacementProcedurePolicyFixture(source, target *core.Engine) catalog.ProcedureRemapper {
	sourceRoles := source.RoleStore.PublishedRead(&source.Catalog.Owner, source.Catalog.Current())
	targetRoles := target.RoleStore.PublishedRead(&target.Catalog.Owner, target.Catalog.Current())
	return catalog.NewProcedureRemapper(target.RoleStore.PrincipalRemap(&target.Catalog, sourceRoles, targetRoles, nil, routine.OperatorSeeded))
}
