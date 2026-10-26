package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgShadowDenied(q string, prepared bool) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for view pg_shadow"})
	s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
	return s
}

var pgShadowDeniedQueries = []string{
	"SELECT * FROM pg_shadow",
	"SELECT usename FROM pg_catalog.pg_shadow",
	"SELECT passwd FROM pg_shadow",
	"SELECT 1 FROM pg_shadow",
	"SELECT count(*) FROM pg_shadow",
	"SELECT * FROM pg_shadow WHERE FALSE",
	"SELECT passwd FROM pg_shadow LIMIT 0",
	"SELECT s.passwd FROM pg_catalog.pg_shadow AS s WHERE FALSE",
	"SELECT s.passwd FROM pg_shadow s JOIN pg_user u ON s.usesysid=u.usesysid WHERE FALSE",
	"SELECT d.passwd FROM (SELECT passwd FROM pg_shadow) d WHERE FALSE",
	"WITH secrets AS (SELECT passwd FROM pg_shadow) SELECT passwd FROM secrets WHERE FALSE",
	"SELECT 1 WHERE EXISTS (SELECT passwd FROM pg_shadow WHERE FALSE)",
	"SELECT * FROM pgs_ordinary_outer WHERE FALSE",
	"SELECT * FROM pgs_ordinary_invoker WHERE FALSE",
}

func pgShadowAuthorizationSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	pgUserCreate(t, p, b, "pgs_ordinary")
	// The PostgreSQL fixture recreates public without its initial PUBLIC USAGE.
	// Make the stored views resolvable before testing their source authority.
	if b.PG { pgUserExec(t, p, b, "GRANT USAGE ON SCHEMA public TO pgs_ordinary") }
	for _, q := range []string{
		"CREATE VIEW pgs_ordinary_inner AS SELECT passwd FROM pg_catalog.pg_shadow",
		"CREATE VIEW pgs_ordinary_outer AS SELECT passwd FROM pgs_ordinary_inner",
		"CREATE VIEW pgs_ordinary_invoker WITH (security_invoker=true) AS SELECT passwd FROM pg_catalog.pg_shadow",
		"GRANT SELECT ON pgs_ordinary_inner,pgs_ordinary_outer,pgs_ordinary_invoker TO pgs_ordinary",
		"ALTER VIEW pgs_ordinary_inner OWNER TO pgs_ordinary",
		"ALTER VIEW pgs_ordinary_outer OWNER TO pgs_ordinary",
	} { pgUserExec(t, p, b, q) }
}

func TestReferencePGShadowAuthorization(t *testing.T) {
	pgUserScenario(t, []string{"pgs_ordinary"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgShadowAuthorizationSetup(t, p, b)
		conn, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
		for _, switchSQL := range []string{"SET ROLE pgs_ordinary", "SET SESSION AUTHORIZATION pgs_ordinary"} {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(switchSQL, 0))
			for _, q := range pgShadowDeniedQueries { for _, prepared := range []bool{false, true} { fixture.RunStep(t, p.Ctx, conn, b.Recorder, pgShadowDenied(q, prepared)) } }
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
		}
	})
	// Exercise a genuinely ordinary authenticator, not just a switched admin.
	fixture.RoleReferenceCleanup(t, []string{"pgs_login_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run("authenticated_"+b.Name, func(t *testing.T) {
			var d *stuntgres.Driver
			dsn := "pg-shadow-authenticator"
			admin := b.DB
			if !b.PG { d = stuntgres.NewDriver(); admin = fixture.RoleScenarioLoginPool(t, d, false, dsn, "postgres") }
			if _, err := admin.ExecContext(p.Ctx, "CREATE ROLE pgs_login_reader LOGIN PASSWORD 'role-core-fixture'"); err != nil { t.Fatal("ordinary authenticator setup failed") }
			pool := fixture.RoleScenarioLoginPool(t, d, b.PG, dsn, "pgs_login_reader")
			conn, err := pool.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
			for _, q := range pgShadowDeniedQueries[:12] { for _, prepared := range []bool{false, true} { fixture.RunStep(t, p.Ctx, conn, b.Recorder, pgShadowDenied(q, prepared)) } }
		})
	}
}

func TestReferencePGShadowPreparedAuthorization(t *testing.T) {
	pgUserScenario(t, []string{"pgs_prepared"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgUserCreate(t, p, b, "pgs_prepared")
		conn, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
		for _, q := range []string{"SELECT passwd FROM pg_shadow WHERE FALSE", "SELECT count(*) FROM pg_shadow WHERE FALSE", "WITH secrets AS (SELECT passwd FROM pg_shadow) SELECT passwd FROM secrets WHERE FALSE"} {
			func() {
				stmt, err := conn.PrepareContext(p.Ctx, q); if err != nil { t.Fatal(err) }; defer stmt.Close()
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				for _, switchSQL := range []string{"SET ROLE pgs_prepared", "SET SESSION AUTHORIZATION pgs_prepared"} {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(switchSQL, 0))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, pgShadowDenied(q, false))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
				}
				var s fixture.Step
				if q == "SELECT count(*) FROM pg_shadow WHERE FALSE" { s = fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}) } else { s = fixture.Select(q, []string{"passwd"}, []string{"TEXT"}, [][]any{}) }
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}()
		}
		// Prepare and fail as the ordinary reader, then retain that exact
		// statement across administrator grant/revoke without switching readers.
		password := pgShadowMD5
		pgShadowSetPassword(t, p, b, "pgs_prepared", &password)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET ROLE pgs_prepared", 0))
		q := "SELECT passwd FROM pg_catalog.pg_shadow WHERE usename=$1"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal("ordinary credential prepare failed") }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		denied := pgShadowDenied(q, false)
		denied.Args = []any{"pgs_prepared"}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		pgUserExec(t, p, b, "GRANT pg_read_all_data TO pgs_prepared")
		pgShadowPassword(t, p, held, false, "pgs_prepared", &password)
		pgUserExec(t, p, b, "REVOKE pg_read_all_data FROM pgs_prepared")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		if err := stmt.Close(); err != nil { t.Fatal("ordinary credential statement close failed") }
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "DROP ROLE pgs_prepared")
	})
}

// Compile-time contract for held query endpoints used by secret assertions.
var _ pgShadowQuerier = (*sql.Conn)(nil)
