package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathNavigateRoutineAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"jpn_reader"})
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		jsonpathNavigateBoth(t, p, b, c, fixture.Select(`SELECT oid::int4 AS oid,pronamespace::int4 AS ns,pronargs::int4 AS nargs,prorettype::int4 AS result,proisstrict,provolatile AS volatility,proparallel AS parallel FROM pg_catalog.pg_proc WHERE oid=4007`, []string{"oid", "ns", "nargs", "result", "proisstrict", "volatility", "parallel"}, []string{"INT4", "INT4", "INT4", "INT4", "BOOL", "CHAR", "CHAR"}, [][]any{{int64(4007), int64(11), int64(4), int64(3802), true, "i", "s"}}))
		qCatalog := `SELECT oid::int4 AS oid,pronamespace::int4 AS ns,proargtypes::text AS args,prorettype::int4 AS result,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,proretset,pronargdefaults::int4 AS defaults FROM pg_catalog.pg_proc WHERE oid=4007`
		catalog := fixture.Select(qCatalog, []string{"oid", "ns", "args", "result", "proisstrict", "volatility", "parallel", "proretset", "defaults"}, []string{"INT4", "INT4", "TEXT", "INT4", "BOOL", "TEXT", "TEXT", "BOOL", "INT4"}, [][]any{{int64(4007), int64(11), `3802 4072 3802 16`, int64(3802), true, "i", "s", false, int64(2)}})
		if b.PG {
			jsonpathNavigateBoth(t, p, b, c, catalog)
		}
		run(fixture.Exec(`CREATE USER jpn_reader`, 0))
		run(fixture.Exec(`CREATE TABLE jpn_acl(doc jsonb)`, 0))
		run(fixture.Exec(`INSERT INTO jpn_acl VALUES('[1,2]')`, 1))
		run(fixture.Exec(`CREATE VIEW jpn_acl_view AS SELECT jsonb_path_query_array(doc,'$[*]') AS changed FROM jpn_acl`, 0))
		run(fixture.Exec(`GRANT SELECT ON jpn_acl,jpn_acl_view TO jpn_reader`, 0))
		if b.PG {
			run(fixture.Exec(`GRANT USAGE ON SCHEMA public TO jpn_reader`, 0))
		}
		signature := `pg_catalog.jsonb_path_query_array(jsonb,jsonpath,jsonb,boolean)`
		// Builtin ACLs are outside public-schema reset. Restore on every failure path.
		defer func() {
			if _, err := c.ExecContext(p.Ctx, `RESET SESSION AUTHORIZATION`); err != nil {
				t.Error(err)
			}
			if _, err := c.ExecContext(p.Ctx, `REVOKE EXECUTE ON FUNCTION `+signature+` FROM jpn_reader`); err != nil {
				t.Error(err)
			}
			if _, err := c.ExecContext(p.Ctx, `GRANT EXECUTE ON FUNCTION `+signature+` TO PUBLIC`); err != nil {
				t.Error(err)
			}
		}()
		run(fixture.Exec(`SET SESSION AUTHORIZATION jpn_reader`, 0))
		q := `SELECT jsonb_path_query_array(doc,'$[*]') AS changed FROM jpn_acl`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		good := jsonpathNavigateSelect(q, `[1, 2]`)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Exec(`REVOKE EXECUTE ON FUNCTION `+signature+` FROM PUBLIC`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION jpn_reader`, 0))
		deny := func(q string) fixture.Step {
			return jsonpathNavigateError(q, "42501", `permission denied for function jsonb_path_query_array`)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, deny(q))
		jsonpathNavigateBoth(t, p, b, c, deny(`SELECT changed FROM jpn_acl_view`))
		jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(`SELECT jsonb_path_query_array(NULL::jsonb,'$') AS changed`, nil))
		jsonpathNavigateBoth(t, p, b, c, deny(q+` WHERE false`))
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Exec(`GRANT EXECUTE ON FUNCTION `+signature+` TO jpn_reader`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION jpn_reader`, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
		jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(`SELECT changed AS changed FROM jpn_acl_view`, `[1, 2]`))
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
	})
}
