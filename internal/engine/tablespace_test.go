package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func tablespaceUnitDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	var d *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !errors.As(err, &d) || *d != want {
		t.Fatalf("diagnostic got %#v (%v), want %#v", d, err, want)
	}
}

func TestTablespaceExecutorNoPublication(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	state, roles, oid, revision, principal := e.catalog.State, e.roles.Published, e.catalog.NextOID(), e.catalog.NextRevision(), e.roles.NextPrincipal()
	identity, authorizationRevision := op.RoleState(), op.Session.Authorization.Revision
	for _, tc := range []struct {
		stmt          parser.CreateTablespace
		code, message string
	}{
		{parser.CreateTablespace{Name: parser.Identifier{Text: "ordinary"}, Location: "/valid"}, "0A000", ddl.TablespacePhysicalUnsupported},
		{parser.CreateTablespace{Name: parser.Identifier{Text: "ordinary"}, Location: "relative"}, "42P17", "tablespace location must be an absolute path"},
		{parser.CreateTablespace{Name: parser.Identifier{Text: "ordinary"}, Owner: &parser.RoleSpec{Name: "missing"}, Location: "relative"}, "42704", "role \"missing\" does not exist"},
	} {
		result, err := e.Execute(op, &tc.stmt, 0, nil)
		tablespaceUnitDiagnostic(t, err, tc.code, tc.message, "")
		if result.Rows != nil || result.CommandTag != "" {
			t.Fatal("refusal exposed result", result)
		}
		if e.catalog.State != state || e.catalog.State.Epoch() != state.Epoch() || e.roles.Published != roles || e.catalog.NextOID() != oid || e.catalog.NextRevision() != revision || e.roles.NextPrincipal() != principal || op.RoleState() != identity || op.Session.Authorization.Revision != authorizationRevision {
			t.Fatal("refusal published catalog/identity/allocator changes")
		}
	}
	original := op.Ctx
	ctx, cancel := context.WithCancel(original)
	cancel()
	op.Ctx = ctx
	_, err := e.Execute(op, &parser.CreateTablespace{Name: parser.Identifier{Text: "ordinary"}, Location: "/valid"}, 0, nil)
	op.Ctx = original
	if !errors.Is(err, context.Canceled) || e.catalog.State != state || e.roles.Published != roles || e.catalog.NextOID() != oid || e.catalog.NextRevision() != revision || e.roles.NextPrincipal() != principal {
		t.Fatal("canceled create published", err)
	}
}
