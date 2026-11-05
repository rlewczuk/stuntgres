package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePostgresFDWConnections(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pf_role_a", "pf_role_b"})
	p, _ := postgresFDWPhysicalScenario(t)
	fixture.ExternalSQLPasswordAuthentication(t, p, "stuntgres", "stuntgres")
	postgresFDWEach(t, p, func(t *testing.T, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		postgresFDWPhysicalInstall(t, b, run)
		first := postgresFDWPID(t, p, c)
		if got := postgresFDWPID(t, p, c); got != first {
			t.Fatalf("default keep_connections lost PID: %d -> %d", first, got)
		}
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		if got := postgresFDWPID(t, p, peer); got == first {
			t.Fatal("different physical sessions shared a backend")
		}
		// Unrelated direct/private/committed metadata and newly created foreign
		// identities cannot retire an already cached mapping's physical backend.
		run(fixture.Exec("COMMENT ON FOREIGN TABLE pf_table IS 'unrelated direct publication'", 0))
		if got := postgresFDWPID(t, p, c); got != first {
			t.Fatal("unrelated direct publication invalidated mapping")
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("COMMENT ON FOREIGN TABLE pf_table IS 'unrelated private publication'", 0))
		if got := postgresFDWPID(t, p, c); got != first {
			t.Fatal("unrelated private publication invalidated mapping")
		}
		run(fixture.Exec("COMMIT", 0))
		if got := postgresFDWPID(t, p, c); got != first {
			t.Fatal("unrelated journal publication invalidated mapping")
		}
		unusedServer := false
		defer func() {
			if unusedServer {
				if _, err := c.ExecContext(p.Ctx, "DROP SERVER IF EXISTS pf_unused CASCADE"); err != nil {
					t.Error(err)
				}
			}
		}()
		run(fixture.Exec("CREATE SERVER pf_unused FOREIGN DATA WRAPPER postgres_fdw", 0))
		unusedServer = true
		run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER pf_unused", 0))
		if got := postgresFDWPID(t, p, c); got != first {
			t.Fatal("new foreign identities invalidated existing mapping")
		}
		run(fixture.Exec("DROP USER MAPPING FOR PUBLIC SERVER pf_unused", 0))
		run(fixture.Exec("DROP SERVER pf_unused", 0))
		unusedServer = false
		if got := postgresFDWPID(t, p, c); got != first {
			t.Fatal("unrelated foreign removal invalidated mapping")
		}
		// Idle invalidation is actual closure, not merely a new cache label.
		run(fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(SET password 'stuntgres')", 0))
		postgresFDWObserveGone(t, p, first)
		second := postgresFDWPID(t, p, c)
		if second == first {
			t.Fatal("invalid mapping reused old backend")
		}
		run(fixture.Exec("ALTER SERVER pf_server OPTIONS(ADD keep_connections 'false')", 0))
		postgresFDWObserveGone(t, p, second)
		run(fixture.Exec("BEGIN", 0))
		third := postgresFDWPID(t, p, c)
		run(fixture.Exec("COMMIT", 0))
		postgresFDWObserveGone(t, p, third)
		run(fixture.Exec("BEGIN", 0))
		fourth := postgresFDWPID(t, p, c)
		run(fixture.Exec("COMMIT", 0))
		if third == fourth {
			t.Fatal("keep_connections=false reused a closed backend")
		}
		postgresFDWObserveGone(t, p, fourth)
		run(fixture.Exec("ALTER SERVER pf_server OPTIONS(SET keep_connections 'true')", 0))
		run(fixture.Exec("BEGIN", 0))
		active := postgresFDWPID(t, p, c)
		run(fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(SET password 'stuntgres')", 0))
		if got := postgresFDWPID(t, p, c); got != active {
			t.Fatal("in-use invalidation closed current transaction")
		}
		run(fixture.Exec("COMMIT", 0))
		postgresFDWObserveGone(t, p, active)
		// Server2/table1 fetch bounds cross all four genuine rows, and the same
		// held cursor is moved backward before later complete/empty fetches.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE pf_cursor SCROLL CURSOR FOR SELECT id,label,flag,bytes FROM pf_table ORDER BY id", 0))
		run(externalSQLProviderRows(b.PG, "FETCH 1 FROM pf_cursor", [][]any{{int64(1), "x", true, "\x00\xff"}}))
		run(externalSQLProviderRows(b.PG, "FETCH 1 FROM pf_cursor", [][]any{{int64(2), nil, false, nil}}))
		run(externalSQLProviderRows(b.PG, "FETCH BACKWARD 1 FROM pf_cursor", [][]any{{int64(1), "x", true, "\x00\xff"}}))
		run(fixture.Exec("MOVE ABSOLUTE 0 FROM pf_cursor", 0))
		run(externalSQLProviderRows(b.PG, "FETCH ALL FROM pf_cursor", externalSQLProviderExpectedRows()))
		run(externalSQLProviderRows(b.PG, "FETCH ALL FROM pf_cursor", nil))
		run(fixture.Exec("CLOSE pf_cursor", 0))
		run(fixture.Exec("ROLLBACK", 0))
		// PUBLIC is selected by mapping identity, not current RoleOID; two local
		// users share one backend, while an exact mapping must take precedence.
		for _, role := range []string{"pf_role_a", "pf_role_b"} {
			run(fixture.Exec("CREATE ROLE "+role, 0))
		}
		defer func() {
			for _, q := range []string{"RESET ROLE", "DROP OWNED BY pf_role_a,pf_role_b CASCADE", "DROP ROLE pf_role_a,pf_role_b"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error(q, err)
				}
			}
		}()
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO pf_role_a,pf_role_b", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER pf_server TO pf_role_a,pf_role_b", 0))
		run(fixture.Exec("GRANT SELECT ON pf_table,pf_identity TO pf_role_a,pf_role_b", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER pf_server OPTIONS(user 'stuntgres',password 'stuntgres')", 0))
		run(fixture.Exec("SET ROLE pf_role_a", 0))
		publicPID := postgresFDWPID(t, p, c)
		run(fixture.Exec("SET ROLE pf_role_b", 0))
		if got := postgresFDWPID(t, p, c); got != publicPID {
			t.Fatal("same PUBLIC mapping did not share physical connection")
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET ROLE pf_role_a", 0))
		run(fixture.Select("SELECT label FROM pf_table WHERE id=1", []string{"label"}, []string{"TEXT"}, [][]any{{"x"}}))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`UPDATE "pf source ""quoted"."data table" SET remote_label='public-outside' WHERE "Remote ID"=1`, 1))
		run(fixture.Exec("SET ROLE pf_role_b", 0))
		run(fixture.Select("SELECT label FROM pf_table WHERE id=1", []string{"label"}, []string{"TEXT"}, [][]any{{"x"}}))
		if got := postgresFDWPID(t, p, c); got != publicPID {
			t.Fatal("PUBLIC roles split one remote transaction")
		}
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Select("SELECT label FROM pf_table WHERE id=1", []string{"label"}, []string{"TEXT"}, [][]any{{"public-outside"}}))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`UPDATE "pf source ""quoted"."data table" SET remote_label='x' WHERE "Remote ID"=1`, 1))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR pf_role_b SERVER pf_server OPTIONS(user 'stuntgres',password 'stuntgres')", 0))
		run(fixture.Exec("SET ROLE pf_role_b", 0))
		exactPID := postgresFDWPID(t, p, c)
		if exactPID == publicPID {
			t.Fatal("exact mapping reused PUBLIC mapping's backend")
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("DROP USER MAPPING FOR pf_role_b SERVER pf_server", 0))
		postgresFDWObserveGone(t, p, exactPID)
		run(fixture.Exec("DROP USER MAPPING FOR PUBLIC SERVER pf_server", 0))
		postgresFDWObserveGone(t, p, publicPID)
	})
}
