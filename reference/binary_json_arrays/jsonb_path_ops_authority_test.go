package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBPathOpsRoutineAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"jpo_reader"})
	jsonbPathOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE USER jpo_reader`, 0))
		run(fixture.Exec(`CREATE TABLE jpo_acl(doc jsonb,path jsonpath)`, 0))
		run(fixture.Exec(`INSERT INTO jpo_acl VALUES('true','$')`, 1))
		run(fixture.Exec(`GRANT SELECT ON jpo_acl TO jpo_reader`, 0))
		run(fixture.Exec(`GRANT USAGE ON SCHEMA public TO jpo_reader`, 0))
		for _, tc := range []struct {
			op, name, alias string
			oid, operator   int
		}{
			{"@?", "jsonb_path_exists_opr", "#?#", 4010, 4012}, {"@@", "jsonb_path_match_opr", "#@#", 4011, 4013},
		} {
			t.Run(tc.op, func(t *testing.T) {
				jsonpathNavigateBoth(t, p, b, c, fixture.Select(fmt.Sprintf(`SELECT oid::int4 AS oid,pronamespace::int4 AS ns,pronargs::int4 AS nargs,prorettype::int4 AS result,proisstrict,provolatile AS volatility,proparallel AS parallel FROM pg_catalog.pg_proc WHERE oid=%d`, tc.oid), []string{"oid", "ns", "nargs", "result", "proisstrict", "volatility", "parallel"}, []string{"INT4", "INT4", "INT4", "INT4", "BOOL", "CHAR", "CHAR"}, [][]any{{int64(tc.oid), int64(11), int64(2), int64(16), true, "i", "s"}}))
				jsonpathNavigateBoth(t, p, b, c, fixture.Select(fmt.Sprintf(`SELECT oid::int4 AS oid,oprleft::int4 AS l,oprright::int4 AS r,oprresult::int4 AS result,oprcode::oid::int4 AS routine FROM pg_catalog.pg_operator WHERE oid=%d`, tc.operator), []string{"oid", "l", "r", "result", "routine"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(tc.operator), int64(3802), int64(4072), int64(16), int64(tc.oid)}}))
				q := fmt.Sprintf(`SELECT proargtypes::text AS args,pronargdefaults::int4 AS defaults,proretset,provariadic::int4 AS variadic FROM pg_catalog.pg_proc WHERE oid=%d`, tc.oid)
				s := fixture.Select(q, []string{"args", "defaults", "proretset", "variadic"}, []string{"TEXT", "INT4", "BOOL", "INT4"}, [][]any{{"3802 4072", int64(0), false, int64(0)}})
				if !b.PG {
					d := fixture.ControlsReferenceDiagnostic("42703", `column "proargtypes" does not exist`)
					d.Position = fixture.Position(strings.Index(q, "proargtypes") + 1)
					s = fixture.ErrorDiagnostic(q, d)
					s.SelectRows = true
				}
				run(s)
				s = fixture.Prepared(s)
				if !b.PG {
					s.ErrorStage = "prepare"
				}
				run(s)
				sig := `pg_catalog.` + tc.name + `(jsonb,jsonpath)`
				defer func() {
					for _, q := range []string{`RESET SESSION AUTHORIZATION`, `REVOKE EXECUTE ON FUNCTION ` + sig + ` FROM jpo_reader`, `GRANT EXECUTE ON FUNCTION ` + sig + ` TO PUBLIC`} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(err)
						}
					}
				}()
				run(fixture.Exec(`CREATE OPERATOR public.`+tc.alias+` (LEFTARG=jsonb,RIGHTARG=jsonpath,PROCEDURE=pg_catalog.`+tc.name+`)`, 0))
				jsonpathNavigateBoth(t, p, b, c, jsonbPathOpsOne(`SELECT doc OPERATOR(public.`+tc.alias+`) path AS changed FROM jpo_acl`, true))
				run(fixture.Exec(`CREATE VIEW jpo_acl_view AS SELECT doc `+tc.op+` path AS changed FROM jpo_acl`, 0))
				run(fixture.Exec(`GRANT SELECT ON jpo_acl_view TO jpo_reader`, 0))
				run(fixture.Exec(`SET SESSION AUTHORIZATION jpo_reader`, 0))
				q = `SELECT doc ` + tc.op + ` $1::jsonpath AS changed FROM jpo_acl`
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				good := jsonbPathOpsOne(q, true, `$`)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
				run(fixture.Exec(`REVOKE EXECUTE ON FUNCTION `+sig+` FROM PUBLIC`, 0))
				run(fixture.Exec(`SET SESSION AUTHORIZATION jpo_reader`, 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateError(q, "42501", `permission denied for function `+tc.name, `$`))
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT changed FROM jpo_acl_view`, "42501", `permission denied for function `+tc.name))
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT doc `+tc.op+` path AS changed FROM jpo_acl`, "42501", `permission denied for function `+tc.name))
				run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
				run(fixture.Exec(`GRANT EXECUTE ON FUNCTION `+sig+` TO jpo_reader`, 0))
				run(fixture.Exec(`SET SESSION AUTHORIZATION jpo_reader`, 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
				run(fixture.Exec(`DROP VIEW jpo_acl_view`, 0))
			})
		}
	})
}
