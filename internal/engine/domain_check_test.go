package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Private catalog/binder/evaluator tests supplement, never duplicate public
// SQL reference scenarios. The staging helpers do not execute driver SQL.
func TestDomainCheckBindingIdentityInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.ordered AS integer CONSTRAINT z_last CHECK (VALUE>0) CONSTRAINT a_first CHECK (VALUE>10)"))
	view := e.CatalogRead()
	d := domainUnitType(t, view, "ordered")
	if len(d.DomainChecks) != 2 || d.DomainChecks[0].Name != "a_first" || d.DomainChecks[0].Ordinal != 1 || d.DomainChecks[1].Name != "z_last" || d.DomainChecks[1].Ordinal != 0 || !d.SQLType.HasDomainChecks() {
		t.Fatal("lost own order/identity", d.DomainChecks)
	}
	if before.State() == view.State() || len(before.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: d.ObjectNamespace, Name: d.Name()})) != 0 {
		t.Fatal("published into immutable prior root")
	}
	if err := catalog.ValidateDefinitionOIDs(view); err != nil {
		t.Fatal(err)
	}
	if err := stored.ValidateDomainCheckDefinitions(view, d); err != nil {
		t.Fatal(err)
	}
	for _, check := range d.DomainChecks {
		if check.Expression.Properties().RowDependent || !check.Expression.Properties().DomainDependent {
			t.Fatal("VALUE is not conversion-dependent", check.Expression.Properties())
		}
		plan.WalkExpression(check.Expression, func(node expr.Expr) {
			if _, value := node.(plan.DomainValueExpr); value && node.ResultUsage() != d.SQLType.DomainBase() {
				t.Fatal("VALUE lost immediate base usage")
			}
		})
	}
	for _, q := range []string{
		"CREATE DOMAIN public.rejected AS integer CONSTRAINT same CHECK (TRUE) CONSTRAINT same CHECK (missing>0)",
		"CREATE DOMAIN public.rejected AS integer CHECK (missing>0)",
	} {
		raw := statement(t, q)
		e.Mu.Lock()
		_, err := stageSchemaLocked(e, op, raw, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
		e.Mu.Unlock()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) {
			t.Fatal("missing binding diagnostic", err)
		}
		if strings.Contains(q, "CONSTRAINT same") {
			if diagnostic.Code != "42710" || diagnostic.Message != `constraint "same" for domain "rejected" already exists` {
				t.Fatal("name-before-bind precedence", diagnostic)
			}
		} else if diagnostic.Code != "42703" || diagnostic.Position != "" {
			t.Fatal("cooked-expression diagnostic", diagnostic)
		}
		if e.CatalogRead().State() != view.State() {
			t.Fatal("failed declaration published")
		}
	}
	long := strings.Repeat("é", 31)
	if got := catalog.GeneratedConstraintName(long, "", "check", map[string]bool{}); got != strings.Repeat("é", 28)+"_check" {
		t.Fatal("UTF8 clipping", got)
	}
	if !catalog.ConstraintNames(view, d.ObjectNamespace, nil)["a_first"] {
		t.Fatal("domain constraint missing from namespace generator")
	}
}

func TestDomainCheckEvaluationFrameInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE DOMAIN public.parent AS integer CONSTRAINT z_parent CHECK (VALUE>0)",
		"CREATE DOMAIN public.middle AS public.parent",
		"CREATE DOMAIN public.child AS public.middle CONSTRAINT a_child CHECK (VALUE>10)",
		"CREATE DOMAIN public.true_value AS integer CHECK (TRUE)",
		"CREATE DOMAIN public.unknown_value AS integer CHECK (NULL::boolean)",
		"CREATE DOMAIN public.false_value AS integer CHECK (FALSE)",
		"CREATE DOMAIN public.inner_value AS integer CHECK (VALUE>0)",
		"CREATE DOMAIN public.outer_value AS integer CHECK ((VALUE+1)::public.inner_value>VALUE AND VALUE=1)",
		"CREATE DOMAIN public.constant_error AS integer CONSTRAINT a_false CHECK (FALSE) CONSTRAINT z_error CHECK (1/0>0)",
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	view := e.CatalogRead()
	integer, _ := view.LookupType(sqltype.Int4)
	boolean, _ := view.LookupType(sqltype.Bool)
	sentinel := sqltype.Boolean(boolean, true)
	eval := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{DomainValue: sentinel, DomainValueActive: true}}
	eval.Input.DomainChecks = &eval
	for _, tc := range []struct {
		domain            string
		n                 int64
		constraint, state string
	}{
		{"child", 0, "z_parent", "23514"}, {"child", 5, "a_child", "23514"}, {"child", 11, "", ""},
		{"true_value", 0, "", ""}, {"unknown_value", 0, "", ""}, {"false_value", 1, "false_value_check", "23514"},
		{"outer_value", 1, "", ""}, {"outer_value", 2, "outer_value_check", "23514"}, {"constant_error", 1, "", "22012"},
	} {
		d := domainUnitType(t, view, tc.domain)
		candidate, err := sqltype.Integer(integer, tc.n)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := sqltype.PlanCoercion(integer.Usage(), d.SQLType.Usage(), sqltype.Explicit)
		if err != nil {
			t.Fatal(err)
		}
		out, err := plan.Apply(eval.Context, candidate, eval.Input, eval.Output)
		if tc.state == "" {
			if err != nil || out.Type() != d.SQLType {
				t.Fatal(tc, out, err)
			}
		} else {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.state || diagnostic.Constraint != tc.constraint {
				t.Fatal(tc, err)
			}
			if tc.state == "23514" && (diagnostic.Schema != "public" || diagnostic.DataType != tc.domain || diagnostic.Table != "" || diagnostic.Detail != "" || diagnostic.Position != "") {
				t.Fatal("domain/ancestor error identity", diagnostic)
			}
		}
		if eval.DomainValue != sentinel || !eval.DomainValueActive {
			t.Fatal("nested/error VALUE frame leaked", tc)
		}
	}
	leaf := plan.DomainValueExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{DomainDependent: true}}}
	if _, err := leaf.Eval(&eval); err == nil {
		t.Fatal("wrong VALUE frame type accepted")
	}
	eval.DomainValueActive = false
	if _, err := leaf.Eval(&eval); err == nil {
		t.Fatal("missing VALUE frame accepted")
	}
	varchar, _ := view.LookupType(sqltype.VarChar)
	short, _, err := varchar.ResolveModifiers(3)
	if err != nil {
		t.Fatal(err)
	}
	long, _, err := varchar.ResolveModifiers(4)
	if err != nil {
		t.Fatal(err)
	}
	textLeaf := plan.DomainValueExpr{Info: expr.Info{Usage: short, Property: expr.Properties{DomainDependent: true}}}
	eval.DomainValue, eval.DomainFrameUsage, eval.DomainValueActive = sqltype.String(varchar, "abc"), long, true
	if _, err := textLeaf.Eval(&eval); err == nil {
		t.Fatal("foreign immediate-base typmod accepted")
	}
	eval.DomainFrameUsage = short
	if value, err := textLeaf.Eval(&eval); err != nil || value != eval.DomainValue {
		t.Fatal("matching VALUE frame", value, err)
	}
	eval.DomainValue, eval.DomainFrameUsage, eval.DomainValueActive = sentinel, sqltype.Usage{}, false
	ctx, cancel := context.WithCancel(eval.Context)
	cancel()
	eval.Context = ctx
	child := domainUnitType(t, view, "child")
	if err := eval.CheckDomain(ctx, child.SQLType, sqltype.Null(integer)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
	if eval.DomainValue != sentinel || eval.DomainValueActive {
		t.Fatal("canceled conversion changed frame")
	}
	if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, leaf); err == nil {
		t.Fatal("VALUE rendered without domain authority")
	}
}

func TestDomainCheckRetirementAndRemapInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE DOMAIN public.provider AS integer",
		"CREATE DOMAIN public.dependent AS integer CONSTRAINT provider_check CHECK (VALUE::public.provider>0)",
		"CREATE DOMAIN public.descendant AS public.dependent CHECK (VALUE<100)",
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	before := e.CatalogRead()
	dependent := domainUnitType(t, before, "dependent")
	stage := domainUnitStage(t, e, op, "DROP DOMAIN public.provider CASCADE")
	edges, err := DomainDependencyClosure(context.Background(), stage, stage.DomainTargets)
	if err != nil {
		t.Fatal(err)
	}
	visible := 0
	for _, edge := range edges {
		if edge.Automatic {
			continue
		}
		visible++
		if edge.Domain != dependent || edge.Label != "constraint provider_check" || edge.Address.ObjectID != dependent.DomainChecks[0].OID {
			t.Fatal("CHECK-owned dependency", edges)
		}
	}
	if visible != 1 {
		t.Fatal("CHECK-owned dependency multiplicity", edges)
	}
	if err := StageExpressionDependencies(context.Background(), stage, stage.DomainTargets, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	d := domainUnitType(t, view, "dependent")
	child := domainUnitType(t, view, "descendant")
	if len(d.DomainChecks) != 0 || d.SQLType.HasDomainChecks() || !child.SQLType.HasDomainChecks() || child.SQLType.DomainBase().Type != d.SQLType || len(dependent.DomainChecks) != 1 || !dependent.SQLType.HasDomainChecks() {
		t.Fatal("retirement lost immutable ancestry")
	}
	if err := stored.ValidateDomainCheckDefinitions(view, child); err != nil {
		t.Fatal(err)
	}
	plan.WalkExpression(child.DomainChecks[0].Expression, func(node expr.Expr) {
		if _, value := node.(plan.DomainValueExpr); value && node.ResultUsage().Type != d.SQLType {
			t.Fatal("VALUE retained old base generation")
		}
	})
	if err := catalog.ValidateDefinitionOIDs(view); err != nil {
		t.Fatal(err)
	}
}
