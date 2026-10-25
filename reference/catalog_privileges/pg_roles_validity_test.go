package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePGRolesValidity(t *testing.T) {
	pgRolesScenario(t, []string{"pgr_time"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgRolesCreate(t, p, b, "pgr_time")
		pgRolesBoth(t, p, b, pgRolesSelect("SELECT * FROM pg_roles WHERE oid=$1", [][]any{pgRolesRow("pgr_time", id, nil)}, id))
		pgRolesExec(t, p, b, "ALTER ROLE pgr_time VALID UNTIL '2030-01-02 03:04:05+00'")
		instant := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		pgRolesBoth(t, p, b, pgRolesSelect("SELECT * FROM pg_roles WHERE oid=$1", [][]any{pgRolesRow("pgr_time", id, instant)}, id))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		query := "SELECT rolvaliduntil::text AS expiry FROM pg_roles WHERE oid=$1"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
		for _, tc := range []struct{ zone, text string }{{"UTC", "2030-01-02 03:04:05+00"}, {"America/New_York", "2030-01-01 22:04:05-05"}} {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
			s := fixture.Select(query, []string{"expiry"}, []string{"TEXT"}, [][]any{{tc.text}}, id)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		old, err := stmt.QueryContext(p.Ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		// pq cannot issue another command while this connection has unread rows.
		// Native rows are detached and additionally witness a same-session change.
		if b.PG {
			pgUserRetainedExpiry(t, old, "2030-01-01 22:04:05-05")
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
		if !b.PG {
			pgUserRetainedExpiry(t, old, "2030-01-01 22:04:05-05")
		}
		if _, err := conn.ExecContext(p.Ctx, "RESET TIME ZONE"); err != nil {
			t.Fatal(err)
		}
		for _, expiry := range []string{"infinity", "-infinity", "2000-01-01 00:00:00+00"} {
			pgRolesExec(t, p, b, "ALTER ROLE pgr_time VALID UNTIL '"+expiry+"'")
			pgRolesBoth(t, p, b, fixture.Select(query, []string{"expiry"}, []string{"TEXT"}, [][]any{{expiry}}, id))
			if expiry == "infinity" || expiry == "-infinity" {
				pgRolesBoth(t, p, b, pgRolesSelect("SELECT * FROM pg_roles WHERE oid=$1", [][]any{pgRolesRow("pgr_time", id, expiry)}, id))
			}
		}
		pgRolesExec(t, p, b, "DROP ROLE pgr_time")
	})
}
