package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func extensionUnitExecute(t *testing.T, e *core.Engine, op *session.Operation, stmt parser.Statement) error {
	t.Helper()
	scope, err := e.StartStatement(op)
	if err != nil {
		return err
	}
	finished := false
	defer func() {
		// A malformed unit AST or an unexpected engine panic must not leave
		// statement admission held while roleOperation closes the session.
		// Preserve the panic; this defer only performs the normal scope unwind.
		if !finished {
			_ = scope.Finish(errors.New("stuntgres: extension unit statement unwound"))
		}
	}()
	_, err = scope.Execute(stmt, 0, nil)
	finished = true
	return scope.Finish(err)
}

func extensionUnitGrant(name string, role string) *parser.ObjectGrant {
	return &parser.ObjectGrant{Kind: "database", Privileges: []parser.GrantPrivilege{{Name: "create"}}, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: name}}}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: role}}}}
}

func extensionUnitError(t *testing.T, err error, code string) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code {
		t.Fatalf("got %v, want SQLSTATE %s", err, code)
	}
}
