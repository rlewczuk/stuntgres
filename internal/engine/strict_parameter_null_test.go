package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Bind actual supported providers under genuine catalog/statement authority.
// SQL creates incidental state; assertions exercise private planning and original
// typed frames, not public SQL response witnesses or synthetic probe providers.
func strictNullUnitFixture(t *testing.T) (*Engine, *Operation, *StatementScope) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	for _, query := range []string{
		"CREATE TABLE strict_null_rows(v integer)",
		"CREATE SEQUENCE strict_null_steps START 1",
		"CREATE DOMAIN strict_null_checked AS integer CONSTRAINT strict_null_present CHECK(VALUE IS NOT NULL)",
	} {
		roleExecute(t, e, op, query)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	result, err := scope.Execute(statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	return e, op, scope
}

func strictNullUnitBind(t *testing.T, e *Engine, op *Operation, scope *StatementScope, text string) (expr.Expr, *expressionBinder, exec.Context) {
	t.Helper()
	raw := statement(t, "SELECT "+text).(*parser.Select).Targets[0]
	ref := roleTableRef(t, e, "strict_null_rows")
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	b := newExpressionBinder(context.Background(), e, scope.input, op, nil)
	b.query.auth, b.target.ref = &auth, ref
	b.target.relation = auth.Catalog.MustRelation(ref.Address)
	b.clause.aggregateClause = "SELECT"
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	e.mu.Unlock()
	if err != nil {
		t.Fatal("actual provider binding", text, err)
	}
	return node, b, exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: hostOf(scope), Catalog: auth.Catalog, Input: scope.input, Output: scope.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.dependencies}}
}

func TestStrictParameterNullPlanning(t *testing.T) {
	planners := []struct {
		name string
		run  func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error)
	}{{"preflight", preflightCoercionsWithPolicy}, {"conditional", simplifyConditional}}
	for _, planner := range planners {
		t.Run(planner.name, func(t *testing.T) {
			e, op, scope := strictNullUnitFixture(t)
			for _, expression := range []string{"abs($1::integer)", "abs(CASE WHEN TRUE THEN $1 ELSE v END)", "abs(($1::smallint)::integer)"} {
				t.Run(expression, func(t *testing.T) {
					node, b, evaluation := strictNullUnitBind(t, e, op, scope, expression)
					evaluation.Parameters = []sqltype.Value{sqltype.Null(b.params.parameters[1].Type)}
					planned, changed, err := planner.run(&evaluation, node, true)
					if err != nil || !changed {
						t.Fatal("known NULL did not prune", changed, err)
					}
					value, err := planned.Eval(&evaluation)
					if err != nil || !value.IsNull() || value.Type() != node.ResultUsage().Type {
						t.Fatal("typed strict result", value, err)
					}
					if planned.ResultUsage() != node.ResultUsage() || planned.Properties() != node.Properties() {
						t.Fatal("lost conservative result/parameter/row authority")
					}
					// Reusing the original analyzed expression with a non-NULL
					// frame must still execute the original selected provider.
					input, err := sqltype.Integer(b.params.parameters[1].Type, -7)
					if err != nil {
						t.Fatal(err)
					}
					evaluation.Parameters[0] = input
					again, _, err := planner.run(&evaluation, node, true)
					if err != nil {
						t.Fatal(err)
					}
					value, err = again.Eval(&evaluation)
					if err != nil || value.IsNull() {
						t.Fatal("original binding was mutated", value, err)
					}
					got, err := value.Integer()
					if err != nil || got != 7 {
						t.Fatal("non-NULL original invocation", got, err)
					}
				})
			}
			// A real volatile sibling is never evaluated merely to discover
			// strict NULL. The sequence supplies an observable effect boundary.
			node, b, evaluation := strictNullUnitBind(t, e, op, scope, "($1::integer)+nextval('strict_null_steps'::regclass)::integer")
			evaluation.Parameters = []sqltype.Value{sqltype.Null(b.params.parameters[1].Type)}
			sequence := catalogRef(t, e, e.CatalogRead(), "strict_null_steps")
			planned, changed, err := planner.run(&evaluation, node, true)
			if err != nil || !changed {
				t.Fatal("volatile sibling strict pruning", changed, err)
			}
			if value, err := planned.Eval(&evaluation); err != nil || !value.IsNull() {
				t.Fatal(value, err)
			}
			e.mu.Lock()
			called := e.storage.Sequences[sequence.Address].Called
			e.mu.Unlock()
			if called {
				t.Fatal("NULL proof evaluated a genuine volatile provider")
			}
			// Ordinary immutable argument failures still precede this rule.
			node, b, evaluation = strictNullUnitBind(t, e, op, scope, "($1::integer)+(1/0)")
			evaluation.Parameters = []sqltype.Value{sqltype.Null(b.params.parameters[1].Type)}
			_, _, err = planner.run(&evaluation, node, true)
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
		})
	}
}

func TestStrictParameterNullBoundaries(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, b, evaluation := strictNullUnitBind(t, e, op, scope, "abs($1::integer)")
	for _, frame := range [][]sqltype.Value{nil, make([]sqltype.Value, 1)} {
		evaluation.Parameters = frame
		if _, folded, err := foldStrictParameterNull(&evaluation, node); err != nil || folded {
			t.Fatal("uninitialized parameter became a NULL constant", folded, err)
		}
	}
	// Runtime row NULL is not an execution-known parameter constant.
	row, _, rowEvaluation := strictNullUnitBind(t, e, op, scope, "abs(v)")
	rowEvaluation.Row = []sqltype.Value{sqltype.Null(b.params.parameters[1].Type)}
	rowEvaluation.Parameters = []sqltype.Value{sqltype.Null(b.params.parameters[1].Type)}
	if _, folded, err := foldStrictParameterNull(&rowEvaluation, row); err != nil || folded {
		t.Fatal("row demand was pruned", folded, err)
	}
	// A strict SRF initializes even with NULL parameters: it is not scalar.
	set, setBinder, setEvaluation := strictNullUnitBind(t, e, op, scope, "generate_series($1::integer,$2::integer)")
	setEvaluation.Parameters = []sqltype.Value{sqltype.Null(setBinder.params.parameters[1].Type), sqltype.Null(setBinder.params.parameters[2].Type)}
	if _, folded, err := foldStrictParameterNull(&setEvaluation, set); err != nil || folded {
		t.Fatal("SRF initialization was pruned", folded, err)
	}
	// A real domain conversion cannot be replaced by the physical NULL of its
	// input; its CHECK truth remains an actual later consumption boundary.
	domain, domainBinder, domainEvaluation := strictNullUnitBind(t, e, op, scope, "abs(($1::integer)::strict_null_checked)")
	domainEvaluation.Parameters = []sqltype.Value{sqltype.Null(domainBinder.params.parameters[1].Type)}
	if _, folded, err := foldStrictParameterNull(&domainEvaluation, domain); err != nil || folded {
		t.Fatal("domain barrier was discarded", folded, err)
	}
	_, err := domain.Eval(&domainEvaluation)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" || diagnostic.Constraint != "strict_null_present" {
		t.Fatal("domain CHECK truth was suppressed", err)
	}
}

func TestStrictParameterNullCompoundSemantics(t *testing.T) {
	for _, planner := range []struct {
		name string
		run  func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error)
	}{{"preflight", preflightCoercionsWithPolicy}, {"conditional", simplifyConditional}} {
		t.Run(planner.name, func(t *testing.T) {
			e, op, scope := strictNullUnitFixture(t)
			for _, tc := range []struct {
				expression string
				want       any
			}{
				{"1 IS DISTINCT FROM $1::integer", true},
				{"NULL::integer IS NOT DISTINCT FROM $1::integer", true},
				{"NULLIF(1,$1::integer)", int64(1)},
				{"GREATEST(1,$1::integer)", int64(1)},
				{"LEAST(1,$1::integer)", int64(1)},
				{"1 IN (1,$1::integer)", true},
				{"1 NOT IN (1,$1::integer)", false},
				{"$1::integer = ANY(ARRAY[]::integer[])", false},
				{"$1::integer = ALL(ARRAY[]::integer[])", true},
			} {
				t.Run(tc.expression, func(t *testing.T) {
					node, b, evaluation := strictNullUnitBind(t, e, op, scope, tc.expression)
					evaluation.Parameters = []sqltype.Value{sqltype.Null(b.params.parameters[1].Type)}
					planned, _, err := planner.run(&evaluation, node, true)
					if err != nil {
						t.Fatal(err)
					}
					value, err := planned.Eval(&evaluation)
					if got := testExport(value); err != nil || got != tc.want {
						t.Fatalf("compound NULL semantics got=%v, err=%v; want %v", got, err, tc.want)
					}
				})
			}
		})
	}
}
