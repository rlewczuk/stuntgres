package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Catalog ACLs survive the public-schema reset. Keep their cleanup scoped to
// these scenarios and run it after statements, borrowers and notice pools close.
func roleGrantCatalogScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step), bool)) {
	t.Helper()
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		if b.PG {
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cleanup, err := b.DB.Conn(ctx)
				if err != nil {
					t.Error("catalog grant cleanup connection", err)
					return
				}
				defer cleanup.Close()
				for _, query := range []string{
					"RESET SESSION AUTHORIZATION",
					"DROP OWNED BY rg_reader,rg_owner,rg_a,rg_b,rg_c,rg_group CASCADE",
				} {
					if _, err := cleanup.ExecContext(ctx, query); err != nil {
						t.Error("catalog grant cleanup", err)
					}
				}
			})
		}
		body(t, p, b, c, run, prepared)
	})
}

func TestReferenceRoleGrantCatalogAdmission(t *testing.T) {
	roleGrantCatalogScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE rg_catalog_anchor(a INT)", 0))
		for _, q := range []string{
			"GRANT SELECT ON TABLE pg_catalog.pg_class TO PUBLIC GRANTED BY CURRENT_USER",
			"GRANT SELECT(relname) ON pg_catalog.pg_class TO rg_reader",
			"GRANT SELECT ON pg_catalog.pg_transform TO rg_b",
			"GRANT ALL PRIVILEGES ON pg_catalog.pg_class TO rg_a WITH GRANT OPTION",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_class TO rg_b", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_b", 0))
		run(fixture.Select("SELECT oid FROM pg_catalog.pg_transform WHERE FALSE", []string{"oid"}, []string{"OID"}, nil))
		run(fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE oid='rg_catalog_anchor'::regclass", []string{"relname"}, []string{"NAME"}, [][]any{{"rg_catalog_anchor"}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT has_table_privilege('rg_a','pg_catalog.pg_class','SELECT WITH GRANT OPTION') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("SET SESSION AUTHORIZATION rg_b", 0))
		fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT SELECT ON pg_catalog.pg_transform TO rg_reader WITH GRANT OPTION",
			fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `no privileges were granted for "pg_transform"`})
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT has_table_privilege('rg_reader','pg_catalog.pg_transform','SELECT WITH GRANT OPTION') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
		run(fixture.Exec("GRANT SELECT(oid) ON pg_catalog.pg_transform TO rg_reader", 0))
		run(fixture.Exec("GRANT ALL ON pg_catalog.pg_transform TO rg_c WITH GRANT OPTION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_transform TO rg_owner WITH GRANT OPTION", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT has_table_privilege('rg_owner','pg_catalog.pg_transform','SELECT WITH GRANT OPTION') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
	})
}

func TestReferenceRoleGrantProviderAdmission(t *testing.T) {
	roleGrantCatalogScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE ROLE rg_future LOGIN", 0))
		// Grants on pg_shadow do not confer rights on its protected source.
		checkAuthid := func() {
			s := fixture.RoleGrantDenied("SELECT rolname FROM pg_catalog.pg_authid WHERE FALSE", "pg_authid")
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
		}
		q := "SELECT usename FROM pg_catalog.pg_shadow WHERE usename='rg_future'"
		run(fixture.Exec("GRANT SELECT ON information_schema.sql_parts TO rg_b", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_b", 0))
		run(fixture.Select("SELECT feature_id::text AS id FROM information_schema.sql_parts WHERE feature_id='1'", []string{"id"}, []string{"TEXT"}, [][]any{{"1"}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		allowed := fixture.Select(q, []string{"usename"}, []string{"NAME"}, [][]any{{"rg_future"}})
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for view pg_shadow"})
		denied.SelectRows, denied.ErrorStage = true, "execute"
		run(denied)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(usename) ON pg_catalog.pg_shadow TO rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(allowed)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, allowed)
		run(fixture.Select("SELECT 1 AS n FROM pg_catalog.pg_shadow WHERE FALSE", []string{"n"}, []string{"INT4"}, nil))
		s := fixture.ImplicitError("SELECT passwd FROM pg_catalog.pg_shadow WHERE FALSE", fixture.Diagnostic{Code: "42501", Message: "permission denied for view pg_shadow"})
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		checkAuthid()
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_shadow TO rg_reader WITH GRANT OPTION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_shadow TO rg_a", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		run(allowed)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, allowed)
		checkAuthid()
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantCatalogAtomicity(t *testing.T) {
	roleGrantCatalogScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE rg_catalog_atomic(a INT)", 0))
		run(fixture.Exec("INSERT INTO rg_catalog_atomic VALUES(7)", 1))
		checkDenied := func() {
			run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
			for _, target := range []struct{ query, name string }{
				{"SELECT a FROM rg_catalog_atomic", "rg_catalog_atomic"},
				{"SELECT usename FROM pg_catalog.pg_shadow WHERE FALSE", "pg_shadow"},
			} {
				s := fixture.RoleGrantDenied(target.query, target.name)
				if target.name == "pg_shadow" {
					s = fixture.ImplicitError(target.query, fixture.Diagnostic{Code: "42501", Message: "permission denied for view pg_shadow"})
				}
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		for _, test := range []struct{ query, code, message string }{
			{"GRANT SELECT ON rg_catalog_atomic,pg_catalog.pg_shadow,rg_catalog_missing TO rg_reader", "42P01", `relation "rg_catalog_missing" does not exist`},
			{"GRANT SELECT(a) ON rg_catalog_atomic,pg_catalog.pg_shadow TO rg_reader", "42703", `column "a" of relation "pg_shadow" does not exist`},
			{"GRANT DELETE(usename) ON pg_catalog.pg_shadow TO rg_reader", "0LP01", "invalid privilege type DELETE for column"},
			{"GRANT SELECT ON rg_catalog_atomic,pg_catalog.pg_shadow TO rg_reader,PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
			{"GRANT SELECT ON rg_catalog_atomic,pg_catalog.pg_shadow TO rg_reader,rg_catalog_missing_role", "42704", `role "rg_catalog_missing_role" does not exist`},
			{"GRANT SELECT ON pg_catalog.pg_shadow TO rg_reader GRANTED BY rg_owner", "0A000", "grantor must be current user"},
			{"GRANT SELECT ON rg_catalog_missing TO rg_reader GRANTED BY rg_catalog_missing_role", "42704", `role "rg_catalog_missing_role" does not exist`},
		} {
			fixture.RoleGrantError(run, test.query, test.code, test.message)
			checkDenied()
		}
		run(fixture.Select("SELECT a FROM rg_catalog_atomic", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}

func TestReferenceRoleGrantCatalogLookalikes(t *testing.T) {
	roleGrantCatalogScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		for _, q := range []string{
			"CREATE TABLE public.pg_shadow(marker INT)",
			"INSERT INTO public.pg_shadow VALUES(7)",
			"ALTER TABLE public.pg_shadow OWNER TO rg_owner",
			"CREATE INDEX pg_class ON public.pg_shadow(marker)",
			"SET SESSION AUTHORIZATION rg_owner",
		} {
			count := int64(0)
			if q == "INSERT INTO public.pg_shadow VALUES(7)" {
				count = 1
			}
			run(fixture.Exec(q, count))
		}
		run(fixture.Exec("GRANT SELECT(marker) ON public.pg_shadow TO rg_reader", 0))
		fixture.RoleGrantError(run, "GRANT SELECT ON pg_catalog.pg_shadow TO rg_reader", "42501", "permission denied for table pg_shadow")
		fixture.RoleGrantError(run, "GRANT SELECT ON public.pg_class TO rg_reader", "42809", `"pg_class" is an index`)
		fixture.RoleGrantError(run, "GRANT SELECT(usename) ON public.pg_shadow TO rg_reader", "42703", `column "usename" of relation "pg_shadow" does not exist`)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Select("SELECT marker FROM public.pg_shadow", []string{"marker"}, []string{"INT4"}, [][]any{{int64(7)}}))
		s := fixture.ImplicitError("SELECT usename FROM pg_catalog.pg_shadow WHERE FALSE", fixture.Diagnostic{Code: "42501", Message: "permission denied for view pg_shadow"})
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
