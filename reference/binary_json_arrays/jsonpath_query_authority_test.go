package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathQueryRoutineAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"jpq_reader"})
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE USER jpq_reader`, 0))
		run(fixture.Exec(`CREATE TABLE jpq_acl_input(doc jsonb)`, 0))
		run(fixture.Exec(`INSERT INTO jpq_acl_input VALUES('true')`, 1))
		run(fixture.Exec(`GRANT SELECT ON jpq_acl_input TO jpq_reader`, 0))
		if b.PG {
			run(fixture.Exec(`GRANT USAGE ON SCHEMA public TO jpq_reader`, 0))
		}
		for _, tc := range []struct {
			name        string
			oid, result int
			set         bool
		}{{"jsonb_path_exists", 4005, 16, false}, {"jsonb_path_match", 4009, 16, false}, {"jsonb_path_query", 4006, 3802, true}, {"jsonb_path_query_first", 4008, 3802, false}} {
			jsonpathNavigateBoth(t, p, b, c, fixture.Select(fmt.Sprintf(`SELECT oid::int4 AS oid,pronamespace::int4 AS ns,pronargs::int4 AS nargs,prorettype::int4 AS result,proisstrict,provolatile AS volatility,proparallel AS parallel FROM pg_catalog.pg_proc WHERE oid=%d`, tc.oid), []string{"oid", "ns", "nargs", "result", "proisstrict", "volatility", "parallel"}, []string{"INT4", "INT4", "INT4", "INT4", "BOOL", "CHAR", "CHAR"}, [][]any{{int64(tc.oid), int64(11), int64(4), int64(tc.result), true, "i", "s"}}))
			q := fmt.Sprintf(`SELECT proargtypes::text AS args,pronargdefaults::int4 AS defaults,proretset FROM pg_catalog.pg_proc WHERE oid=%d`, tc.oid)
			s := fixture.Select(q, []string{"args", "defaults", "proretset"}, []string{"TEXT", "INT4", "BOOL"}, [][]any{{`3802 4072 3802 16`, int64(2), tc.set}})
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
			func() {
				sig := `pg_catalog.` + tc.name + `(jsonb,jsonpath,jsonb,boolean)`
				defer func() {
					for _, q := range []string{`RESET SESSION AUTHORIZATION`, `REVOKE EXECUTE ON FUNCTION ` + sig + ` FROM jpq_reader`, `GRANT EXECUTE ON FUNCTION ` + sig + ` TO PUBLIC`} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(err)
						}
					}
				}()
				run(fixture.Exec(`CREATE VIEW jpq_acl_view AS SELECT `+tc.name+`(doc,'$') AS changed FROM jpq_acl_input`, 0))
				run(fixture.Exec(`GRANT SELECT ON jpq_acl_view TO jpq_reader`, 0))
				run(fixture.Exec(`SET SESSION AUTHORIZATION jpq_reader`, 0))
				// Keep a real runtime operand even when PG specializes bound parameters.
				q := `SELECT ` + tc.name + `(doc,$1,$2,$3) AS changed FROM jpq_acl_input`
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				typ := "BOOL"
				want := any(true)
				if tc.result == 3802 {
					typ = "JSONB"
					want = `true`
				}
				good := jsonpathQueryOne(q, typ, want, `$`, `{}`, false)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
				run(fixture.Exec(`REVOKE EXECUTE ON FUNCTION `+sig+` FROM PUBLIC`, 0))
				run(fixture.Exec(`SET SESSION AUTHORIZATION jpq_reader`, 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateError(q, "42501", `permission denied for function `+tc.name, `$`, `{}`, false))
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT changed FROM jpq_acl_view`, "42501", `permission denied for function `+tc.name))
				run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
				run(fixture.Exec(`GRANT EXECUTE ON FUNCTION `+sig+` TO jpq_reader`, 0))
				run(fixture.Exec(`SET SESSION AUTHORIZATION jpq_reader`, 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
				run(fixture.Exec(`DROP VIEW jpq_acl_view`, 0))
			}()
		}
	})
}
