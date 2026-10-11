//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterRenameIfExistsUnauthorizedNamespace(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rename_conditional_reader"})
	p := fixture.NewPair(t)
	fixture.SchemaReferenceCleanup(t, "rename_conditional_schema")
	p.Step(t, fixture.Exec("CREATE USER rename_conditional_reader", 0))
	p.Step(t, fixture.Exec("CREATE SCHEMA rename_conditional_schema CREATE TABLE t(a INT)", 0))
	for _, postgres := range []bool{true, false} {
		name := "memory"
		if postgres {
			name = "postgres"
		}
		// Build the notice pool with the parent test name: the native fixture's
		// opaque DSN is name-scoped, so a subtest name selects a different engine.
		var notices []fixture.Diagnostic
		_, conn, _ := fixture.NoticeReferencePool(t, p, postgres, &notices)
		t.Run(name, func(t *testing.T) {
			if _, err := conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION rename_conditional_reader"); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"RENAME a TO b", "RENAME TO renamed"} {
				q := "ALTER TABLE IF EXISTS rename_conditional_schema.missing " + suffix
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("conditional Prepare checked namespace", err)
				}
				if len(notices) != 0 {
					t.Fatal("conditional Prepare emitted notices", notices)
				}
				_, err = stmt.ExecContext(p.Ctx)
				stmt.Close()
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema rename_conditional_schema"}
				fixture.AssertDiagnostic(t, err, &want)
				if len(notices) != 0 {
					t.Fatal("unauthorized IF EXISTS emitted skip notice", notices)
				}
			}
			if _, err := conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
