package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDomainCheckPreflightDemandInternal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, ddl := range []string{
		"CREATE DOMAIN public.preflight_positive AS integer CHECK(VALUE>0)",
		"CREATE DOMAIN public.preflight_required AS integer CHECK(VALUE IS NOT NULL)",
		"CREATE DOMAIN public.preflight_recipe AS integer CONSTRAINT a_false CHECK(FALSE) CONSTRAINT z_error CHECK(1/0>0)",
		"CREATE DOMAIN public.preflight_descendant AS public.preflight_recipe",
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, ddl))
	}
	bind := func(query string) (expr.Expr, EvalContext) {
		t.Helper()
		raw := statement(t, query).(*parser.Select).Targets[0]
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		b := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
		b.queryAuth = &auth
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		return node, EvalContext{Context: op.Context(), catalog: auth.catalog, Input: b.input}
	}
	for _, query := range []string{
		"SELECT 0::public.preflight_positive",
		"SELECT (0+0)::public.preflight_positive",
		"SELECT (0::public.preflight_positive)::integer+1",
		"SELECT ARRAY[0::public.preflight_positive]",
		"SELECT ARRAY[0]::public.preflight_positive[]",
		"SELECT abs((0::public.preflight_positive)::integer)",
		"SELECT COALESCE(0::public.preflight_positive,1::public.preflight_positive)",
	} {
		t.Run(query, func(t *testing.T) {
			node, evaluation := bind(query)
			p := node.Properties()
			if !p.RuntimeDomainConversion || p.DomainDependent || p.Volatility != expr.Immutable {
				t.Fatal("runtime barrier conflated with VALUE or volatility", p)
			}
			prepared, _, err := preflightCoercions(&evaluation, node)
			if err != nil {
				t.Fatal("preflight consumed CHECK truth", err)
			}
			if _, literal := prepared.(literalExpression); literal || !prepared.Properties().RuntimeDomainConversion {
				t.Fatal("checked conversion/enclosing node folded away")
			}
			_, err = prepared.Eval(&evaluation)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" {
				t.Fatal("demanded truth was not checked", err)
			}
		})
	}
	physical, physicalContext := bind("SELECT (1::integer)::text")
	physical, _, err := preflightCoercions(&physicalContext, physical)
	if err != nil {
		t.Fatal(err)
	}
	if _, literal := physical.(literalExpression); !literal || physical.Properties().RuntimeDomainConversion {
		t.Fatal("unconstrained physical conversion lost fast folding")
	}
	// The runtime folding barrier is legal in stored expressions. It does not
	// grant the distinct authority required to render/evaluate free VALUE.
	node, evaluation := bind("SELECT 1::public.preflight_positive")
	if _, err := (scalarRenderer{}).sql(op.Context(), evaluation.catalog, node); err != nil {
		t.Fatal("stored checked conversion rejected as free VALUE", err)
	}
	required := domainUnitType(t, e.CatalogRead(), "preflight_required").SQLType
	fresh := domainNullInputExpression(required.Usage())
	prepared, _, err := preflightCoercions(&evaluation, fresh)
	if err != nil || !prepared.Properties().RuntimeDomainConversion {
		t.Fatal("fresh NULL consumed during initialization", err)
	}
	_, err = prepared.Eval(&evaluation)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" {
		t.Fatal("fresh NULL truth bypassed", err)
	}
	for _, target := range []sqltype.Usage{required.Usage(), required.DomainBase()} {
		plan, err := sqltype.PlanCoercion(required.Usage(), target, sqltype.Explicit)
		if err != nil || plan.ChecksDomain() {
			t.Fatal("identity/downcast acquired fresh CHECK demand", err)
		}
	}
	// Initialization still prepares every CHECK recipe, even though candidate
	// truth is not executed. A later constant error precedes a_false.
	for _, query := range []string{
		"SELECT 1::public.preflight_recipe",
		"SELECT CASE WHEN TRUE THEN (1::public.preflight_recipe)::integer ELSE 2 END",
		"SELECT CASE WHEN FALSE THEN 2 ELSE (1::public.preflight_recipe)::integer END",
		"SELECT COALESCE((1::public.preflight_recipe)::integer, 2)",
		"SELECT COALESCE((1::public.preflight_descendant)::integer, 2)",
		"SELECT COALESCE((ARRAY[1]::public.preflight_recipe[])[1]::integer, 2)",
	} {
		t.Run(query, func(t *testing.T) {
			node, evaluation := bind(query)
			_, _, err := preflightCoercions(&evaluation, node)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
				t.Fatal("retained recipe initialization lost constant error", err)
			}
		})
	}
	for _, query := range []string{
		"SELECT CASE WHEN FALSE THEN (1::public.preflight_recipe)::integer ELSE 1 END",
		"SELECT COALESCE(1, (1::public.preflight_recipe)::integer)",
	} {
		t.Run(query, func(t *testing.T) {
			node, evaluation := bind(query)
			prepared, _, err := preflightCoercions(&evaluation, node)
			if err != nil {
				t.Fatal("eliminated conversion initialized CHECK recipes", err)
			}
			value, err := prepared.Eval(&evaluation)
			n, numberErr := value.Integer()
			if err != nil || numberErr != nil || n != 1 {
				t.Fatal(value, err, numberErr)
			}
		})
	}
}
