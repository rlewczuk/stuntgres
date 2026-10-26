package reference_test

import (
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgAuthIDDenied(q string, prepared bool) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_authid"})
	s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
	return s
}

var pgAuthIDDeniedQueries = []string{
	"SELECT * FROM pg_authid",
	"SELECT rolname FROM pg_catalog.pg_authid",
	"SELECT rolpassword FROM pg_authid",
	"SELECT 1 FROM pg_authid",
	"SELECT count(*) FROM pg_authid",
	"SELECT * FROM pg_authid WHERE FALSE",
	"SELECT rolpassword FROM pg_authid LIMIT 0",
	"SELECT s.rolpassword FROM pg_catalog.pg_authid AS s WHERE FALSE",
	"SELECT s.rolname FROM pg_authid s JOIN pg_roles u ON s.oid=u.oid WHERE FALSE",
	"SELECT d.rolpassword FROM (SELECT rolpassword FROM pg_authid) d WHERE FALSE",
	"WITH secrets AS (SELECT rolpassword FROM pg_authid) SELECT rolpassword FROM secrets WHERE FALSE",
	"SELECT 1 WHERE EXISTS (SELECT rolpassword FROM pg_authid WHERE FALSE)",
}

func TestReferencePGAuthIDAuthorization(t *testing.T) {
	pgUserScenario(t, []string{"pga_ordinary"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgUserCreate(t, p, b, "pga_ordinary")
		for _, switchSQL := range []string{"SET ROLE pga_ordinary", "SET SESSION AUTHORIZATION pga_ordinary"} {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(switchSQL, 0))
			for _, q := range pgAuthIDDeniedQueries {
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, pgAuthIDDenied(q, prepared))
				}
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("ordinary prepare failed")
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, pgAuthIDDenied(q, false))
				stmt.Close()
			}
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
			conn.Close()
		}
		for _, option := range []string{"CREATEROLE", "CREATEDB", "REPLICATION", "BYPASSRLS"} {
			pgUserExec(t, p, b, "ALTER ROLE pga_ordinary "+option)
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET ROLE pga_ordinary", 0))
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, pgAuthIDDenied("SELECT rolpassword FROM pg_authid WHERE FALSE", prepared))
			}
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
			conn.Close()
			pgUserExec(t, p, b, "ALTER ROLE pga_ordinary NO"+option)
		}
		pgUserExec(t, p, b, "GRANT pg_monitor TO pga_ordinary")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET ROLE pga_ordinary", 0))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, pgAuthIDDenied("SELECT rolname FROM pg_authid", false))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "REVOKE pg_monitor FROM pga_ordinary")
	})
	// Exercise a genuinely ordinary authenticator, not just a switched admin.
	fixture.RoleReferenceCleanup(t, []string{"pga_login_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run("authenticated_"+b.Name, func(t *testing.T) {
			var d *stuntgres.Driver
			dsn := "pg-authid-authenticator"
			admin := b.DB
			if !b.PG {
				d = stuntgres.NewDriver()
				admin = fixture.RoleScenarioLoginPool(t, d, false, dsn, "postgres")
			}
			if _, err := admin.ExecContext(p.Ctx, "CREATE ROLE pga_login_reader LOGIN PASSWORD 'role-core-fixture'"); err != nil {
				t.Fatal("ordinary authenticator setup failed")
			}
			pool := fixture.RoleScenarioLoginPool(t, d, b.PG, dsn, "pga_login_reader")
			conn, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			for _, q := range pgAuthIDDeniedQueries[:12] {
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, pgAuthIDDenied(q, prepared))
				}
			}
		})
	}
}

func TestReferencePGAuthIDPreparedAuthorization(t *testing.T) {
	pgUserScenario(t, []string{"pga_prepared"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgUserCreate(t, p, b, "pga_prepared")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, q := range []string{"SELECT rolpassword FROM pg_authid WHERE FALSE", "SELECT count(*) FROM pg_authid WHERE FALSE", "WITH secrets AS (SELECT rolpassword FROM pg_authid) SELECT rolpassword FROM secrets WHERE FALSE"} {
			func() {
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				for _, switchSQL := range []string{"SET ROLE pga_prepared", "SET SESSION AUTHORIZATION pga_prepared"} {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(switchSQL, 0))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, pgAuthIDDenied(q, false))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
				}
				var s fixture.Step
				if q == "SELECT count(*) FROM pg_authid WHERE FALSE" {
					s = fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
				} else {
					s = fixture.Select(q, []string{"rolpassword"}, []string{"TEXT"}, [][]any{})
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}()
		}
		// Prepare and fail as the ordinary reader, then retain that exact
		// statement across administrator grant/revoke without switching readers.
		password := pgShadowMD5
		pgShadowSetPassword(t, p, b, "pga_prepared", &password)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET ROLE pga_prepared", 0))
		q := "SELECT rolpassword FROM pg_catalog.pg_authid WHERE rolname=$1"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("ordinary credential prepare failed")
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		denied := pgAuthIDDenied(q, false)
		denied.Args = []any{"pga_prepared"}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		pgUserExec(t, p, b, "GRANT pg_read_all_data TO pga_prepared")
		pgAuthIDPassword(t, p, held, false, "pga_prepared", &password)
		pgUserExec(t, p, b, "REVOKE pg_read_all_data FROM pga_prepared")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		if err := stmt.Close(); err != nil {
			t.Fatal("ordinary credential statement close failed")
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "DROP ROLE pga_prepared")
	})
	pgAuthIDAuthenticatedPrepared(t)
}

func TestReferencePGAuthIDDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, tc := range []struct{ query, position string }{
				{"SELECT absent FROM pg_catalog.pg_authid", "8"},
				{"SELECT /*é*/ absent FROM pg_catalog.pg_authid", "14"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42703", Message: `column "absent" does not exist`, Position: tc.position})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		})
	}
}

func pgAuthIDAuthenticatedPrepared(t *testing.T) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"pga_held_login", "pga_held_secret"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run("held_authenticated_"+b.Name, func(t *testing.T) {
			var driver *stuntgres.Driver
			dsn := "pg-authid-held-authenticator"
			admin := b.DB
			if !b.PG {
				driver = stuntgres.NewDriver()
				admin = fixture.RoleScenarioLoginPool(t, driver, false, dsn, "postgres")
			}
			if _, err := admin.ExecContext(p.Ctx, "CREATE ROLE pga_held_login LOGIN PASSWORD 'role-core-fixture'"); err != nil {
				t.Fatal("ordinary login setup failed")
			}
			if _, err := admin.ExecContext(p.Ctx, "CREATE ROLE pga_held_secret"); err != nil {
				t.Fatal("secret role setup failed")
			}
			authority := b
			authority.DB = admin
			password := pgShadowMD5
			pgShadowSetPassword(t, p, authority, "pga_held_secret", &password)
			reader := fixture.RoleScenarioLoginPool(t, driver, b.PG, dsn, "pga_held_login")
			func() {
				conn, err := reader.Conn(p.Ctx)
				if err != nil {
					t.Fatal("ordinary connection failed")
				}
				defer conn.Close()
				q := "SELECT rolpassword FROM pg_catalog.pg_authid WHERE rolname=$1"
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("ordinary prepared description failed")
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				denied := pgAuthIDDenied(q, false)
				denied.Args = []any{"pga_held_secret"}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
				pgUserExec(t, p, authority, "GRANT pg_read_all_data TO pga_held_login")
				pgAuthIDPassword(t, p, held, false, "pga_held_secret", &password)
				pgUserExec(t, p, authority, "REVOKE pg_read_all_data FROM pga_held_login")
				fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
			}()
			for _, name := range []string{"pga_held_secret", "pga_held_login"} {
				pgUserExec(t, p, authority, "DROP ROLE "+name)
			}
		})
	}
}
