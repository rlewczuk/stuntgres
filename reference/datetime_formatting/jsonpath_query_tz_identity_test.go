package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathQueryTZVolatility(t *testing.T) {
	for _, f := range jsonpathQueryTZFunctions {
		if f.set {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE TABLE jpqtz_vol(doc jsonb)`, 0))
				run(fixture.Exec(`INSERT INTO jpqtz_vol VALUES('true')`, 1))
				call := f.name + `(doc,'$')`
				run(fixture.ErrorDiagnostic(`CREATE INDEX jpqtz_bad ON jpqtz_vol(`+call+`)`, fixture.ControlsReferenceDiagnostic("42P17", "functions in index expression must be marked IMMUTABLE")))
				for _, storage := range []string{"STORED", "VIRTUAL"} {
					typ := "jsonb"
					if f.typ == "BOOL" {
						typ = "boolean"
					}
					run(fixture.ErrorDiagnostic(`CREATE TABLE jpqtz_generated(doc jsonb,v `+typ+` GENERATED ALWAYS AS (`+call+`) `+storage+`)`, fixture.ControlsReferenceDiagnostic("42P17", "generation expression is not immutable")))
				}
				ordinary := strings.TrimSuffix(f.name, "_tz")
				run(fixture.Exec(`CREATE INDEX jpqtz_ok ON jpqtz_vol(`+ordinary+`(doc,'$'))`, 0))
				typ := "jsonb"
				if f.typ == "BOOL" {
					typ = "boolean"
				}
				run(fixture.Exec(`CREATE TABLE jpqtz_immutable(doc jsonb,v `+typ+` GENERATED ALWAYS AS (`+ordinary+`(doc,'$')) STORED)`, 0))
			})
		})
	}
}

func TestReferenceJSONPathQueryTZRoutineAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"jpqtz_reader"})
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE USER jpqtz_reader`, 0))
		run(fixture.Exec(`CREATE TABLE jpqtz_acl(doc jsonb)`, 0))
		run(fixture.Exec(`INSERT INTO jpqtz_acl VALUES('true')`, 1))
		run(fixture.Exec(`GRANT SELECT ON jpqtz_acl TO jpqtz_reader`, 0))
		if b.PG {
			run(fixture.Exec(`GRANT USAGE ON SCHEMA public TO jpqtz_reader`, 0))
		}
		for _, f := range jsonpathQueryTZFunctions {
			result := 3802
			if f.typ == "BOOL" {
				result = 16
			}
			q := fmt.Sprintf(`SELECT oid::int4 AS oid,pronamespace::int4 AS ns,pronargs::int4 AS nargs,prorettype::int4 AS result,proisstrict,provolatile AS volatility,proparallel AS parallel FROM pg_catalog.pg_proc WHERE oid=%d`, f.oid)
			fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(q, []string{"oid", "ns", "nargs", "result", "proisstrict", "volatility", "parallel"}, []string{"INT4", "INT4", "INT4", "INT4", "BOOL", "CHAR", "CHAR"}, [][]any{{int64(f.oid), int64(11), int64(4), int64(result), true, "s", "s"}}))
			// Private declaration units cover fields absent from native pg_proc.
			q = fmt.Sprintf(`SELECT proargtypes::text AS args,pronargdefaults::int4 AS defaults,proretset FROM pg_catalog.pg_proc WHERE oid=%d`, f.oid)
			if b.PG {
				fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(q, []string{"args", "defaults", "proretset"}, []string{"TEXT", "INT4", "BOOL"}, [][]any{{`3802 4072 3802 16`, int64(2), f.set}}))
			}
			func() {
				sig := `pg_catalog.` + f.name + `(jsonb,jsonpath,jsonb,boolean)`
				defer func() {
					for _, q := range []string{`RESET SESSION AUTHORIZATION`, `REVOKE EXECUTE ON FUNCTION ` + sig + ` FROM jpqtz_reader`, `GRANT EXECUTE ON FUNCTION ` + sig + ` TO PUBLIC`} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(err)
						}
					}
				}()
				run(fixture.Exec(`CREATE VIEW jpqtz_acl_view AS SELECT `+f.name+`(doc,'$') AS changed FROM jpqtz_acl`, 0))
				run(fixture.Exec(`GRANT SELECT ON jpqtz_acl_view TO jpqtz_reader`, 0))
				run(fixture.Exec(`SET SESSION AUTHORIZATION jpqtz_reader`, 0))
				q := `SELECT ` + f.name + `(doc,$1,$2,$3) AS changed FROM jpqtz_acl`
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				want := any(true)
				if f.typ == "JSONB" {
					want = `true`
				}
				if f.name == "jsonb_path_query_array_tz" {
					want = `[true]`
				}
				good := jsonpathQueryOne(q, f.typ, want, `$`, `{}`, false)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
				run(fixture.Exec(`REVOKE EXECUTE ON FUNCTION `+sig+` FROM PUBLIC`, 0))
				run(fixture.Exec(`SET SESSION AUTHORIZATION jpqtz_reader`, 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateError(q, "42501", `permission denied for function `+f.name, `$`, `{}`, false))
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT changed FROM jpqtz_acl_view`, "42501", `permission denied for function `+f.name))
				run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
				run(fixture.Exec(`GRANT EXECUTE ON FUNCTION `+sig+` TO jpqtz_reader`, 0))
				run(fixture.Exec(`SET SESSION AUTHORIZATION jpqtz_reader`, 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
				run(fixture.Exec(`DROP VIEW jpqtz_acl_view`, 0))
			}()
		}
	})
}

func TestReferenceJSONPathQueryTZConsumers(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonpathQueryTZStored(t, p, b, c)
		fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(`SELECT jsonb_path_exists_tz(doc,'$') AS changed,count(*) AS n FROM jpqtz_stored GROUP BY jsonb_path_exists_tz(doc,'$')`, []string{"changed", "n"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(1)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ALTER TABLE jpqtz_stored RENAME COLUMN doc TO payload`, 0))
		fixture.JSONPathNavigateBoth(t, p, b, c, check)
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT CASE WHEN present THEN COALESCE(first,'0'::jsonb) ELSE '9'::jsonb END AS changed FROM jpqtz_scalar`, "JSONB", jsonpathQueryTZDocument))
	})
}
