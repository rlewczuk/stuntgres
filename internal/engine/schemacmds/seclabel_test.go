package schemacmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestSecurityLabelNoPublication(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	if _, err := e.Execute(op, &parser.CreateRole{Name: parser.Identifier{Text: "sl_reader"}}, 0, nil); err != nil { t.Fatal(err) }
	for _, ordinary := range []bool{false, true} {
		if ordinary {
			if _, err := e.Execute(op, &parser.RoleSelection{Name: "sl_reader"}, 0, nil); err != nil { t.Fatal(err) }
		}
		state, roles := e.Catalog.State, e.RoleStore.Published
		oid, revision, principal := e.Catalog.NextOID(), e.Catalog.NextRevision(), e.RoleStore.NextPrincipal()
		identity, authorizationRevision := op.RoleState(), op.Session.Authorization.Revision
		unchanged := func() {
			t.Helper()
			if e.Catalog.State != state || e.RoleStore.Published != roles || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.RoleStore.NextPrincipal() != principal || op.RoleState() != identity || op.Session.Authorization.Revision != authorizationRevision { t.Fatal("SECURITY LABEL published catalog/role/allocator/identity changes") }
		}
		for _, provider := range []*parser.Identifier{nil, {Text: ""}, {Text: "missing"}, {Text: "dummy"}, {Text: "selinux"}, {Text: "a\"b"}} {
			stmt := &parser.SecurityLabel{ProviderSpecified: provider != nil, Kind: "index", Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "absent"}}}, Null: true}
			if provider != nil {
				stmt.Provider = *provider
			}
			if _, err := e.Describe(op, stmt); err != nil { t.Fatal("Describe executed provider resolution", err) }
			unchanged()
			result, err := e.Execute(op, stmt, 0, nil)
			message := "no security label providers have been loaded"
			if provider != nil { message = "security label provider \"" + provider.Text + "\" is not loaded" }
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: message}
			var got *sqlstate.Error
			if !errors.As(err, &got) || *got != want { t.Fatalf("diagnostic got=%#v (%v) want=%#v", got, err, want) }
			if result.Rows != nil || result.CommandTag != "" { t.Fatal("failed utility published result", result) }
			unchanged()
		}
		original := op.Ctx
		ctx, cancel := context.WithCancel(original)
		cancel()
		op.Ctx = ctx
		_, describeErr := e.Describe(op, &parser.SecurityLabel{})
		_, executeErr := e.Execute(op, &parser.SecurityLabel{}, 0, nil)
		op.Ctx = original
		if !errors.Is(describeErr, context.Canceled) || !errors.Is(executeErr, context.Canceled) { t.Fatal("cancellation precedence", describeErr, executeErr) }
		unchanged()
	}
}
