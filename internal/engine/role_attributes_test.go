package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func roleAttributeCreate(t *testing.T, e *core.Engine, op *session.Operation, name string, login bool, o parser.RoleOptions) authorization.RoleDescriptor {
	t.Helper()
	roleAttributeApply(t, e, op, &parser.CreateRole{Name: parser.Identifier{Text: name}, DefaultLogin: login, Options: o})
	d, ok := e.Roles().Lookup(name)
	if !ok {
		t.Fatal("missing role", name)
	}
	return d
}

func roleAttributeApply(t *testing.T, e *core.Engine, op *session.Operation, s parser.Statement) {
	t.Helper()
	r, err := e.Execute(op, s, 0, nil)
	if err != nil || r.Count != 0 || r.Rows != nil {
		t.Fatalf("attribute utility: result=%#v error=%v", r, err)
	}
}

func roleAttributeFailure(t *testing.T, e *core.Engine, op *session.Operation, s parser.Statement, code string) {
	t.Helper()
	beforePublication := e.RoleStore.Published
	next := e.RoleStore.NextPrincipal()
	revision := e.RoleStore.NextRevision()
	r, err := e.Execute(op, s, 0, nil)
	var d *sqlstate.Error
	if err == nil || code != "" && (!errors.As(err, &d) || d.Code != code) || r.Rows != nil {
		t.Fatalf("want %s: %#v %v", code, r, err)
	}
	if e.RoleStore.Published != beforePublication || e.RoleStore.NextPrincipal() != next || e.RoleStore.NextRevision() != revision {
		t.Fatal("failed mutation published role root or allocator")
	}
}
