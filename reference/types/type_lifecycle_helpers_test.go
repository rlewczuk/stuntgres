package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// All lifecycle selectors share this body and public fixture across backends.
func typeLifecycleScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, *[]fixture.Diagnostic)) {
	t.Helper()
	for _, schema := range []string{"tl_src", "tl_dst", "tl_other", "tl_missing"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
					t.Error(err)
				}
			}()
			body(t, p, b, c, r, &notices)
		})
	}
}

func typeLifecycleHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatalf("Prepare %s: %v", q, err)
	}
	t.Cleanup(func() { s.Close() })
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
}

func typeLifecycleFailure(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, want fixture.Diagnostic) {
	t.Helper()
	fixture.CompositeReferenceError(t, p, c, q, "execute", want)
}

func typeLifecycleNotice(message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message}
}

func typeLifecycleCount(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, q string, n int64, args ...any) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{n}}, args...))
}

// Resolve generated companions through real catalog identity, never a guessed
// underscore spelling; displacement can change the name without changing its ID.
func typeLifecycleCompanion(t *testing.T, p *fixture.Pair, c *sql.Conn, base string) (int64, string, string) {
	t.Helper()
	array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='"+base+"'::regtype::oid")
	var namespace, name string
	if err := c.QueryRowContext(p.Ctx, "SELECT n.nspname,t.typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE t.oid=$1::oid", array).Scan(&namespace, &name); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	return array, name, quote(namespace) + "." + quote(name)
}
