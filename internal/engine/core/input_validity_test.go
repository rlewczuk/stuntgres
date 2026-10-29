package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestInputValidityDeclarationUsage(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := core.CaptureRegtypeInput(auth, op)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		declaration string
		id          sqltype.ID
		typmod      int32
		array       bool
	}{
		{"integer", sqltype.Int4, -1, false}, {"pg_catalog.int4", sqltype.Int4, -1, false},
		{"numeric(7,4)", sqltype.Numeric, 458760, false}, {"numeric(8,4)", sqltype.Numeric, 524296, false},
		{"varchar(3)", sqltype.VarChar, 7, false}, {"varchar(3)[]", 1015, 7, true},
		{"numeric(7,4)[]", 1231, 458760, true},
	} {
		usage, err := resolver.ResolveInputUsage(context.Background(), f.declaration)
		if err != nil {
			t.Fatal(f, err)
		}
		expected, ok := auth.Catalog.LookupType(f.id)
		if !ok || usage.Type != expected || usage.Typmod != f.typmod || usage.Type.IsArray() != f.array {
			t.Fatal("usage/generation", f, usage)
		}
	}
	for _, text := range []string{"23", "-"} {
		if _, err := resolver.ResolveInputUsage(context.Background(), text); err == nil {
			t.Fatal("REGTYPE shortcut accepted as declaration", text)
		}
		oid, err := resolver.ResolveRegtype(context.Background(), text)
		want := uint32(23)
		if text == "-" {
			want = 0
		}
		if err != nil || oid != want {
			t.Fatal("REGTYPE value behavior changed", text, oid, err)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolver.ResolveInputUsage(canceled, "integer"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestInputValidityExecutionBoundary(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	scope := core.NewStatementScope(op, op.Session.Transactions, op.StatementStart())
	scope.RoutineAuth = auth
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
	c.Catalog = auth.Catalog
	for _, f := range []struct{ value, target, code, message, detail, hint string }{
		{"42", "integer", "", "", "", ""},
		{"oops", "integer", "22P02", `invalid input syntax for type integer: "oops"`, "", ""},
		{"1234.567", "numeric(7,4)", "22003", "numeric field overflow", "A field with precision 7, scale 4 must round to an absolute value less than 10^3.", ""},
		{"1234.567", "numeric(8,4)", "", "", "", ""},
		{"bogus", "aclitem", "22P02", `unrecognized key word: "bogus"`, "", `ACL key word must be "group" or "user".`},
		{"{1,oops}", "integer[]", "22P02", `invalid input syntax for type integer: "oops"`, "", ""},
	} {
		diagnostic, err := c.CheckTypeInput(f.value, f.target)
		if err != nil {
			t.Fatal(f, err)
		}
		if c.Input.TypeDiagnosticName != nil || c.Input.Regtype != nil || c.Input.ACLItem != nil || c.Input.DomainChecks != nil {
			t.Fatal("conversion leaked its captured environment into the caller")
		}
		if f.code == "" {
			if diagnostic != nil {
				t.Fatal("stale diagnostic", f, diagnostic)
			}
			continue
		}
		if diagnostic == nil || diagnostic.Code != f.code || diagnostic.Message != f.message || diagnostic.Detail != f.detail || diagnostic.Hint != f.hint {
			t.Fatal(f, diagnostic)
		}
	}
	for _, f := range []struct{ target, code string }{{"__iv_missing__", "42704"}, {"pg_ddl_command", "0A000"}, {"numeric(0)", "22023"}, {"int4(", "42601"}} {
		diagnostic, err := c.CheckTypeInput("42", f.target)
		var d *sqlstate.Error
		if diagnostic != nil || !errors.As(err, &d) || d.Code != f.code {
			t.Fatal("setup/hard error became recoverable", f, diagnostic, err)
		}
		if c.Input.TypeDiagnosticName != nil || c.Input.Regtype != nil || c.Input.ACLItem != nil || c.Input.DomainChecks != nil {
			t.Fatal("hard failure leaked its captured environment into the caller")
		}
	}
	// Explicit producer wrappers have precedence over any SQLSTATE in their cause.
	for _, code := range []string{"22P02", "22003", "23514"} {
		cause := &sqlstate.Error{Code: code, Message: "producer diagnostic", Detail: "detail", Hint: "hint", InternalQuery: "query", InternalPosition: "3"}
		soft := sqlstate.SoftInputError(cause)
		if !sqltype.InputSoftError(soft) || sqltype.InputSoftError(sqlstate.HardInputError(soft)) {
			t.Fatal("marker precedence", code)
		}
		var got *sqlstate.Error
		if !errors.As(sqlstate.HardInputError(soft), &got) || got != cause {
			t.Fatal("hard marker lost full diagnostic")
		}
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("ordinary Go error")} {
		if sqltype.InputSoftError(err) {
			t.Fatal("ordinary error converted", err)
		}
	}
}
