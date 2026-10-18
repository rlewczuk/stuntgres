package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
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
	if len(d.domainChecks) != 2 || d.domainChecks[0].Name != "a_first" || d.domainChecks[0].ordinal != 1 || d.domainChecks[1].Name != "z_last" || d.domainChecks[1].ordinal != 0 || !d.typ.HasDomainChecks() {
		t.Fatal("lost own order/identity", d.domainChecks)
	}
	if before.state == view.state || len(before.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: d.namespace, Name: d.Name()})) != 0 {
		t.Fatal("published into immutable prior root")
	}
	if err := validateDefinitionOIDs(view); err != nil {
		t.Fatal(err)
	}
	if err := validateDomainCheckDefinitions(view, d); err != nil {
		t.Fatal(err)
	}
	for _, check := range d.domainChecks {
		if check.expression.Properties().RowDependent || !check.expression.Properties().DomainDependent {
			t.Fatal("VALUE is not conversion-dependent", check.expression.Properties())
		}
		walkBoundExpression(check.expression, func(node BoundExpr) {
			if _, value := node.(domainValueExpression); value && node.ResultUsage() != d.typ.DomainBase() {
				t.Fatal("VALUE lost immediate base usage")
			}
		})
	}
	for _, q := range []string{
		"CREATE DOMAIN public.rejected AS integer CONSTRAINT same CHECK (TRUE) CONSTRAINT same CHECK (missing>0)",
		"CREATE DOMAIN public.rejected AS integer CHECK (missing>0)",
	} {
		raw := statement(t, q)
		e.mu.Lock()
		_, err := e.stageSchemaLocked(op, raw, sqltype.InputEnvironment{})
		e.mu.Unlock()
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
		if e.CatalogRead().state != view.state {
			t.Fatal("failed declaration published")
		}
	}
	long := strings.Repeat("é", 31)
	if got := generatedConstraintName(long, "", "check", map[string]bool{}); got != strings.Repeat("é", 28)+"_check" {
		t.Fatal("UTF8 clipping", got)
	}
	if !constraintNames(view, d.namespace, nil)["a_first"] {
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
	eval := EvalContext{Context: context.Background(), catalog: view, domainValue: sentinel, domainValueActive: true}
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
		plan, err := sqltype.PlanCoercion(integer.Usage(), d.typ.Usage(), sqltype.Explicit)
		if err != nil {
			t.Fatal(err)
		}
		out, err := plan.Apply(eval.Context, candidate, eval.Input, eval.Output)
		if tc.state == "" {
			if err != nil || out.Type() != d.typ {
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
		if eval.domainValue != sentinel || !eval.domainValueActive {
			t.Fatal("nested/error VALUE frame leaked", tc)
		}
	}
	leaf := domainValueExpression{expressionInfo: expressionInfo{usage: integer.Usage(), properties: ExprProperties{DomainDependent: true}}}
	if _, err := leaf.Eval(&eval); err == nil {
		t.Fatal("wrong VALUE frame type accepted")
	}
	eval.domainValueActive = false
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
	textLeaf := domainValueExpression{expressionInfo: expressionInfo{usage: short, properties: ExprProperties{DomainDependent: true}}}
	eval.domainValue, eval.domainFrameUsage, eval.domainValueActive = sqltype.String(varchar, "abc"), long, true
	if _, err := textLeaf.Eval(&eval); err == nil {
		t.Fatal("foreign immediate-base typmod accepted")
	}
	eval.domainFrameUsage = short
	if value, err := textLeaf.Eval(&eval); err != nil || value != eval.domainValue {
		t.Fatal("matching VALUE frame", value, err)
	}
	eval.domainValue, eval.domainFrameUsage, eval.domainValueActive = sentinel, sqltype.Usage{}, false
	ctx, cancel := context.WithCancel(eval.Context)
	cancel()
	eval.Context = ctx
	child := domainUnitType(t, view, "child")
	if err := eval.CheckDomain(ctx, child.typ, sqltype.Null(integer)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
	if eval.domainValue != sentinel || eval.domainValueActive {
		t.Fatal("canceled conversion changed frame")
	}
	if _, err := (scalarRenderer{}).sql(context.Background(), view, leaf); err == nil {
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
	edges, err := domainDependencyClosure(context.Background(), stage, stage.domainTargets)
	if err != nil {
		t.Fatal(err)
	}
	visible := 0
	for _, edge := range edges {
		if edge.automatic {
			continue
		}
		visible++
		if edge.domain != dependent || edge.label != "constraint provider_check" || edge.address.ObjectID != dependent.domainChecks[0].OID {
			t.Fatal("CHECK-owned dependency", edges)
		}
	}
	if visible != 1 {
		t.Fatal("CHECK-owned dependency multiplicity", edges)
	}
	if err := stageExpressionDependencies(context.Background(), stage, stage.domainTargets, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	d := domainUnitType(t, view, "dependent")
	child := domainUnitType(t, view, "descendant")
	if len(d.domainChecks) != 0 || d.typ.HasDomainChecks() || !child.typ.HasDomainChecks() || child.typ.DomainBase().Type != d.typ || len(dependent.domainChecks) != 1 || !dependent.typ.HasDomainChecks() {
		t.Fatal("retirement lost immutable ancestry")
	}
	if err := validateDomainCheckDefinitions(view, child); err != nil {
		t.Fatal(err)
	}
	walkBoundExpression(child.domainChecks[0].expression, func(node BoundExpr) {
		if _, value := node.(domainValueExpression); value && node.ResultUsage().Type != d.typ {
			t.Fatal("VALUE retained old base generation")
		}
	})
	if err := validateDefinitionOIDs(view); err != nil {
		t.Fatal(err)
	}
}
