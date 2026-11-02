package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMaterializedStorageBulkBuiltin(t *testing.T) {
	materializedStorageRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		materializedStorageSeed(run)
		for _, tc := range []struct{ q, code, message string }{
			{"ALTER MATERIALIZED VIEW ALL IN TABLESPACE absent_space OWNED BY absent_role SET TABLESPACE absent_dest", "42704", `role "absent_role" does not exist`},
			{"ALTER MATERIALIZED VIEW ALL IN TABLESPACE absent_space SET TABLESPACE absent_dest", "42704", `tablespace "absent_space" does not exist`},
			{"ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default SET TABLESPACE absent_dest", "42704", `tablespace "absent_dest" does not exist`},
			{"ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_global SET TABLESPACE pg_default", "22023", "cannot move relations in to or out of pg_global tablespace"},
			{"ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default SET TABLESPACE pg_global", "22023", "cannot move relations in to or out of pg_global tablespace"},
			{"ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_global SET TABLESPACE pg_global", "22023", "cannot move relations in to or out of pg_global tablespace"},
		} {
			run(fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message)))
			materializedStorageBaseline(run)
		}
		materializedStorageRoles(run)
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		tx, err := holder.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, materializedStorageRows())
		var before []byte
		if !b.PG {
			before = materializedStorageDump(t, p)
		}
		run(fixture.Exec("SET ROLE ms_reader", 0))
		// Equal endpoints return before ownership selection and AX acquisition.
		for _, owners := range []string{"", " OWNED BY CURRENT_USER", " OWNED BY SESSION_USER", " OWNED BY ms_owner,ms_owner"} {
			run(fixture.Exec("ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default"+owners+" SET TABLESPACE pg_default NOWAIT", 0))
		}
		run(fixture.Exec("RESET ROLE", 0))
		if !b.PG && !bytes.Equal(before, materializedStorageDump(t, p)) {
			t.Fatal("equal bulk rewrote persistent state")
		}
		var notices []fixture.Diagnostic
		_, nc, nr := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, nc, nr, fixture.Exec("ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default OWNED BY ms_reader SET TABLESPACE pg_default NOWAIT", 0))
		if len(notices) != 0 {
			t.Fatalf("equal endpoints emitted notices %+v", notices)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		materializedStorageBaseline(run)
		if !b.PG {
			run(fixture.Select("SELECT spcname FROM pg_tablespace ORDER BY oid", []string{"spcname"}, []string{"NAME"}, [][]any{{"pg_default"}, {"pg_global"}}))
			run(fixture.ErrorDiagnostic("CREATE TABLESPACE ms_physical LOCATION '/tmp/ms_physical'", fixture.ControlsReferenceDiagnostic("0A000", "CREATE TABLESPACE physical locations are unsupported in embedded mode")))
			run(fixture.ErrorDiagnostic("ALTER MATERIALIZED VIEW ALL IN TABLESPACE ms_src SET TABLESPACE ms_dst NOWAIT", fixture.ControlsReferenceDiagnostic("42704", `tablespace "ms_src" does not exist`)))
			materializedStorageBaseline(run)
		}
	})
}

func TestReferenceMaterializedStorageBulkLiveRoles(t *testing.T) {
	materializedStorageRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		materializedStorageSeed(run)
		materializedStorageRoles(run)
		for _, identity := range []string{"CURRENT_USER", "CURRENT_ROLE", "SESSION_USER"} {
			q := "ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default OWNED BY " + identity + " SET TABLESPACE pg_default NOWAIT"
			run(fixture.Exec("SET ROLE ms_owner", 0))
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			executor := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
			for _, role := range []string{"ms_other", "ms_member", "ms_reader"} {
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("SET ROLE "+role, 0))
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 0))
			}
			s.Close()
			run(fixture.Exec("RESET ROLE", 0))
		}
		q := `ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default OWNED BY "current_user" SET TABLESPACE pg_default`
		s, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		executor := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
		want := fixture.ControlsReferenceDiagnostic("42704", `role "current_user" does not exist`)
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.ErrorDiagnostic(q, want))
		run(fixture.Exec(`CREATE ROLE "current_user"`, 0))
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 0))
		run(fixture.Exec(`DROP ROLE "current_user"`, 0))
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.ErrorDiagnostic(q, want))
		materializedStorageBaseline(run)
	})
}
