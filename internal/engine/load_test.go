package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func loadUnitDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("LOAD diagnostic: got %#v (%v), want %#v", got, err, want)
	}
}

func TestLoadValidation(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	if _, err := e.Execute(admin, &parser.CreateRole{Name: parser.Identifier{Text: "load_reader"}, DefaultLogin: true}, 0, nil); err != nil {
		t.Fatal(err)
	}
	reader, closeReader := roleOperation(t, e, "load_reader")
	defer closeReader()
	for _, tc := range []struct {
		filename      string
		ordinary      bool
		code, message string
	}{
		{"", false, "0A000", ddl.LoadUnsupported},
		{"$libdir", false, "0A000", ddl.LoadUnsupported},
		{"$unknown", false, "0A000", ddl.LoadUnsupported},
		{"/an/absolute/name", false, "0A000", ddl.LoadUnsupported},
		{"relative/name", false, "0A000", ddl.LoadUnsupported},
		{"$bad/name", false, "42602", "invalid macro name in path: $bad/name"},
		{"$LIBDIR/name", false, "42602", "invalid macro name in path: $LIBDIR/name"},
		{"$libdir/plugins/", true, "0A000", ddl.LoadUnsupported},
		{"$libdir/plugins/..", true, "0A000", ddl.LoadUnsupported},
		{"$libdir/plugins/\\name", true, "0A000", ddl.LoadUnsupported},
		{"$libdir/plugins/name", true, "0A000", ddl.LoadUnsupported},
		{"$bad/name", true, "42501", "access to library \"$bad/name\" is not allowed"},
		{"$libdir/plugins/../name", true, "42501", "access to library \"$libdir/plugins/../name\" is not allowed"},
		{"$libdir/plugins//name", true, "42501", "access to library \"$libdir/plugins//name\" is not allowed"},
		{"$LIBDIR/plugins/name", true, "42501", "access to library \"$LIBDIR/plugins/name\" is not allowed"},
		{"quo\"te\nname", true, "42501", "access to library \"quo\"te\nname\" is not allowed"},
	} {
		op := admin
		if tc.ordinary {
			op = reader
		}
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		if err == nil {
			err = ddl.Load(op.Context(), auth.View, &parser.Load{Filename: tc.filename})
		}
		e.mu.Unlock()
		loadUnitDiagnostic(t, err, tc.code, tc.message)
	}
}

func TestLoadExecutorNoPublication(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	if _, err := e.Execute(op, &parser.CreateRole{Name: parser.Identifier{Text: "load_reader"}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	for _, ordinary := range []bool{false, true} {
		if ordinary {
			if _, err := e.Execute(op, &parser.RoleSelection{Name: "load_reader"}, 0, nil); err != nil {
				t.Fatal(err)
			}
		}
		state, roles, oid, revision, principal := e.catalog.State, e.roles.published, e.catalog.NextOID(), e.catalog.NextRevision(), e.roles.nextPrincipal
		identity, authorizationRevision := op.RoleState(), op.Session.Authorization.Revision
		unchanged := func() {
			t.Helper()
			if e.catalog.State != state || e.catalog.State.Epoch() != state.Epoch() || e.roles.published != roles || e.catalog.NextOID() != oid || e.catalog.NextRevision() != revision || e.roles.nextPrincipal != principal || op.RoleState() != identity || op.Session.Authorization.Revision != authorizationRevision {
				t.Fatal("LOAD published catalog/role/allocator/identity changes")
			}
		}
		for _, filename := range []string{"library", "$bad/name", "$libdir/plugins/name"} {
			stmt := &parser.Load{Filename: filename}
			if _, err := e.Describe(op, stmt); err != nil {
				t.Fatal("effect-free LOAD Describe:", err)
			}
			unchanged()
			result, err := e.Execute(op, stmt, 0, nil)
			code, message := "0A000", ddl.LoadUnsupported
			if ordinary && filename != "$libdir/plugins/name" {
				code, message = "42501", "access to library \""+filename+"\" is not allowed"
			} else if filename == "$bad/name" {
				code, message = "42602", "invalid macro name in path: $bad/name"
			}
			loadUnitDiagnostic(t, err, code, message)
			if result.Rows != nil || result.CommandTag != "" {
				t.Fatal("LOAD error published a result", result)
			}
			unchanged()
		}
		original := op.Ctx
		ctx, cancel := context.WithCancel(original)
		cancel()
		op.Ctx = ctx
		_, describeErr := e.Describe(op, &parser.Load{Filename: "$libdir/plugins/name"})
		_, executeErr := e.Execute(op, &parser.Load{Filename: "$libdir/plugins/name"}, 0, nil)
		op.Ctx = original
		if !errors.Is(describeErr, context.Canceled) || !errors.Is(executeErr, context.Canceled) {
			t.Fatal("LOAD cancellation", describeErr, executeErr)
		}
		unchanged()
	}
}
