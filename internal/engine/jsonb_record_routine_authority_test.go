package engine

import (
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func jsonbRecordRoutineUnitFixture(t *testing.T) (*Engine, *session.Operation, *StatementScope) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	for _, query := range []string{
		"CREATE TABLE strict_null_rows(v integer)",
		"CREATE DOMAIN strict_null_checked AS integer CONSTRAINT strict_null_present CHECK(VALUE IS NOT NULL)",
		"CREATE VIEW jsonb_authority_recipe AS SELECT * FROM jsonb_to_record('{}'::jsonb) AS j(a integer,b text)",
	} {
		roleExecute(t, e, op, query)
	}
	roleAttributeCreate(t, e, op, "jsonb_authority_owner", false, parser.RoleOptions{Present: parser.RoleLogin, Login: true})
	roleAttributeCreate(t, e, op, "jsonb_authority_reader", false, parser.RoleOptions{Present: parser.RoleLogin, Login: true})
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

func eMustJSONBRoutineAuthority(t *testing.T, e *Engine, op *session.Operation) *AuthorizationView {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	return &auth
}
