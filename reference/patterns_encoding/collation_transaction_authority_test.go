package reference_test

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCollationTransactionAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"coll_tx_actor", "coll_tx_other"})
	fixture.SchemaReferenceCleanup(t, "coll_tx_auth")
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.CollationExec(t, p, b, "CREATE ROLE coll_tx_actor NOSUPERUSER", "CREATE ROLE coll_tx_other NOSUPERUSER", "CREATE SCHEMA coll_tx_auth", "GRANT USAGE,CREATE ON SCHEMA coll_tx_auth TO coll_tx_actor", `CREATE COLLATION coll_tx_auth.existing FROM pg_catalog."C"`, "ALTER COLLATION coll_tx_auth.existing OWNER TO coll_tx_actor")
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		for _, q := range []string{`CREATE COLLATION coll_tx_auth.fresh FROM pg_catalog."C"`, "ALTER COLLATION coll_tx_auth.existing RENAME TO renamed", "ALTER COLLATION coll_tx_auth.existing REFRESH VERSION", "ALTER COLLATION coll_tx_auth.existing OWNER TO coll_tx_other", "ALTER COLLATION coll_tx_auth.existing SET SCHEMA public", "DROP COLLATION coll_tx_auth.existing"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("quiet Prepare", err)
			}
			defer stmt.Close()
			for _, held := range []bool{false, true} {
				run := func(s fixture.Step) {
					var x fixture.Executor = c
					if held {
						x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
				}
				run(fixture.Exec("BEGIN READ ONLY", 0))
				command := strings.Fields(q)[0]
				s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+command+" COLLATION in a read-only transaction"))
				s.ErrorStage = "execute"
				run(s)
				run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
				run(fixture.Exec("ROLLBACK", 0))
			}
		}
		// Prepare with ownership, then revoke it before the retained Execute.
		for _, q := range []string{"ALTER COLLATION coll_tx_auth.existing RENAME TO renamed", "DROP COLLATION coll_tx_auth.existing"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE coll_tx_actor", 0))
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			fixture.CollationExec(t, p, b, "ALTER COLLATION coll_tx_auth.existing OWNER TO coll_tx_other")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
			name := "existing"
			if strings.HasPrefix(q, "DROP ") {
				name = "coll_tx_auth.existing"
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "must be owner of collation "+name)))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
			stmt.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
			fixture.CollationExec(t, p, b, "ALTER COLLATION coll_tx_auth.existing OWNER TO coll_tx_actor")
		}
	})
}

func TestReferenceCollationTransactionNotices(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
		run(fixture.Exec(`CREATE COLLATION coll_tx_version (PROVIDER=builtin,LOCALE='C',VERSION='stale')`, 0))
		// The incumbent version family specifies the creation warning in full.
		notices = nil
		for _, tc := range []struct {
			q    string
			want fixture.Diagnostic
		}{
			{"ALTER COLLATION coll_tx_version REFRESH VERSION", fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "changing version from stale to 1"}},
			{`CREATE COLLATION IF NOT EXISTS coll_tx_version FROM pg_catalog."C"`, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `collation "coll_tx_version" already exists, skipping`}},
			{"DROP COLLATION IF EXISTS coll_tx_absent", fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `collation "coll_tx_absent" does not exist, skipping`}},
		} {
			run(fixture.Exec("BEGIN", 0))
			stmt, err := c.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			if len(notices) != 0 {
				t.Fatal("Prepare emitted notices", notices)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}, recorder, fixture.Exec(tc.q, 0))
			stmt.Close()
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{tc.want}) {
				t.Fatalf("notices=%#v want=%#v", notices, tc.want)
			}
			run(fixture.Exec("ROLLBACK", 0))
			notices = nil
			run(fixture.Select("SELECT collversion FROM pg_collation WHERE collname='coll_tx_version'", []string{"collversion"}, []string{"TEXT"}, [][]any{{"stale"}}))
		}
	})
}
