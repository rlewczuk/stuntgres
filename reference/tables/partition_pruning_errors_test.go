package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePartitionPruningErrors(t *testing.T) {
	referencePruningScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		referencePruningRangeSetup(run)
		for _, c := range []struct {
			query      string
			diagnostic fixture.Diagnostic
		}{
			{"SELECT missing FROM pp_range WHERE k=99", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}},
			{"SELECT k FROM pp_range WHERE k=TRUE", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "31"}},
		} {
			s := fixture.ImplicitError(c.query, c.diagnostic)
			s.SelectRows = true
			run(s)
		}
		run(fixture.Exec("CREATE USER pp_reader NOSUPERUSER NOBYPASSRLS", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO pp_reader", 0))
		run(fixture.Exec("CREATE SCHEMA pp_private", 0))
		run(fixture.Exec("CREATE TABLE pp_private.hidden(k INT) PARTITION BY LIST(k)", 0))
		run(fixture.Exec("CREATE TABLE pp_private.hidden_one PARTITION OF pp_private.hidden FOR VALUES IN(1)", 0))
		run(fixture.Exec("GRANT SELECT ON pp_private.hidden TO pp_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA pp_private TO pp_reader", 0))
		run(fixture.Exec("REVOKE USAGE ON SCHEMA pp_private FROM pp_reader", 0))
		denied := func(query, message string, fields fixture.Diagnostic) {
			s := referenceRangeError(query, "42501", message, fields)
			s.ErrorStage = "" // Schema resolution and execution ACL checks have different frontiers.
			s.SelectRows = true
			run(s)
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION pp_reader", 0))
		denied("SELECT k FROM pp_range WHERE k=99", "permission denied for table pp_range", fixture.Diagnostic{})
		denied("SELECT k FROM pp_private.hidden WHERE k=99", "permission denied for schema pp_private", fixture.Diagnostic{Position: "15"})
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON pp_range TO pp_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pp_reader", 0))
		referencePruningKeys(run, b.PG, "SELECT k FROM pp_range WHERE k=1", int64(1), int64(1))
		denied("SELECT k FROM pp_range_a WHERE k=99", "permission denied for table pp_range_a", fixture.Diagnostic{})
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER TABLE pp_range ENABLE ROW LEVEL SECURITY", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pp_reader", 0))
		referencePruningKeys(run, b.PG, "SELECT k FROM pp_range WHERE k=1")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("CREATE POLICY pp_visible ON pp_range FOR SELECT TO pp_reader USING(k=3)", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pp_reader", 0))
		if b.PG {
			referencePruningKeys(run, true, "SELECT k FROM pp_range WHERE k=1 OR k=3", int64(3))
		} else {
			// Policy-expression enforcement is an existing native capability
			// boundary; pruning must not suppress it even for empty candidates.
			for _, query := range []string{"SELECT k FROM pp_range WHERE k=1 OR k=3", "SELECT k FROM pp_range WHERE k=99"} {
				s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "row-security policy expression enforcement is unsupported"})
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON pp_range FROM pp_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pp_reader", 0))
		denied("SELECT k FROM pp_range WHERE k=99", "permission denied for table pp_range", fixture.Diagnostic{})
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Domain import is required even though the source is childless.
		run(fixture.Exec("CREATE DOMAIN pp_positive AS INT CHECK(VALUE>0)", 0))
		run(fixture.Exec("CREATE TABLE pp_empty(k INT) PARTITION BY RANGE(k)", 0))
		s := fixture.ImplicitError("SELECT k FROM pp_empty WHERE k=$1::pp_positive", fixture.Diagnostic{Code: "23514", Message: `value for domain pp_positive violates check constraint "pp_positive_check"`, Context: "unnamed portal parameter $1 = '...'", Schema: "public", DataType: "pp_positive", Constraint: "pp_positive_check"}, -1)
		s.SelectRows = true
		run(s)
		// PostgreSQL supports physical partition row locks; the native engine's
		// existing explicit boundary is retained, not falsely compared to PG.
		if !b.PG {
			s := fixture.ImplicitError("SELECT k FROM pp_range WHERE k=99 FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking partition hierarchies is unsupported"})
			s.SelectRows = true
			run(s)
		}
	}, func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pp_reader"})
		fixture.SchemaReferenceCleanup(t, "pp_private")
	})
}
