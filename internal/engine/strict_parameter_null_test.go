package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Bind actual supported providers under genuine catalog/statement authority.
// SQL creates incidental state; assertions exercise private planning and original
// typed frames, not public SQL response witnesses or synthetic probe providers.
func strictNullUnitFixture(t *testing.T) (*core.Engine, *session.Operation, *core.StatementScope) {
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

func strictNullUnitBind(t *testing.T, e *core.Engine, op *session.Operation, scope *core.StatementScope, text string) (expr.Expr, *analyze.Binder, exec.Context) {
	t.Helper()
	raw := statement(t, "SELECT "+text).(*parser.Select).Targets[0]
	ref := roleTableRef(t, e, "strict_null_rows")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b := core.NewExpressionBinder(context.Background(), e, scope.Input, op, nil)
	b.Query.Auth, b.Target.Ref = &auth, ref
	b.Target.Relation = auth.Catalog.MustRelation(ref.Address)
	bindTestTargetNamespace(b)
	b.Clause.AggregateClause = "SELECT"
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal("actual provider binding", text, err)
	}
	return node, b, exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
}
