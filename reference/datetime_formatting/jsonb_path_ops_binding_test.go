package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBPathOpsBinding(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "jpo_hidden")
	fixture.RoleReferenceCleanup(t, []string{"jpo_bind_reader"})
	jsonbPathOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE DOMAIN jpo_doc AS jsonb`, 0))
		run(fixture.Exec(`CREATE DOMAIN jpo_path AS jsonpath`, 0))
		for _, op := range jsonbPathOps {
			for _, operands := range [][2]string{{`'true'::jsonb`, `'$'::jsonpath`}, {`'true'`, `'$'::jsonpath`}, {`'true'::jsonb`, `'$'`}, {`'true'::jpo_doc`, `'$'::jpo_path`}} {
				for _, spelling := range []string{op, `OPERATOR(pg_catalog.` + op + `)`} {
					fixture.JSONPathNavigateBoth(t, p, b, c, jsonbPathOpsOne(`SELECT `+operands[0]+` `+spelling+` `+operands[1]+` AS changed`, true))
				}
			}
			for _, tc := range []struct{ left, right, sig string }{
				{`'true'::text`, `'$'::jsonpath`, `text ` + op + ` jsonpath`},
				{`'true'::json`, `'$'::jsonpath`, `json ` + op + ` jsonpath`},
				{`ARRAY[1]`, `'$'::jsonpath`, `integer[] ` + op + ` jsonpath`},
				{`'true'::jsonb`, `'$'::text`, `jsonb ` + op + ` text`},
			} {
				t.Run(op+"/"+tc.sig, func(t *testing.T) {
					q := `SELECT ` + tc.left + ` ` + op + ` ` + tc.right
					jsonpathQueryBindError(t, p, b, c, q, "42883", `operator does not exist: `+tc.sig, op, `No operator matches the given name and argument types. You might need to add explicit type casts.`)
				})
			}
			for _, tc := range []struct{ name, q, code, message, token, hint string }{
				{"unary", `SELECT ` + op + ` 'true'::jsonb`, "42883", `operator does not exist: ` + op + ` jsonb`, op, `No operator matches the given name and argument type. You might need to add an explicit type cast.`},
				{"missing-schema", `SELECT 'true'::jsonb OPERATOR(jpo_missing.` + op + `) '$'::jsonpath`, "3F000", `schema "jpo_missing" does not exist`, "OPERATOR", ""},
				{"empty-schema", `SELECT 'true'::jsonb OPERATOR(public.` + op + `) '$'::jsonpath`, "42883", `operator does not exist: jsonb public.` + op + ` jsonpath`, "OPERATOR", `No operator matches the given name and argument types. You might need to add explicit type casts.`},
				{"suffix", `SELECT 'true'::jsonb ` + op + ` '$'::jsonpath; garbage`, "42601", `syntax error at or near "garbage"`, "garbage", ""},
				{"empty-identifier", `SELECT 'true'::jsonb OPERATOR("".` + op + `) '$'::jsonpath`, "42601", `zero-length delimited identifier at or near """"`, `""`, ""},
			} {
				t.Run(op+"/"+tc.name, func(t *testing.T) { jsonpathQueryBindError(t, p, b, c, tc.q, tc.code, tc.message, tc.token, tc.hint) })
			}
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonbPathOpsOne(`SELECT 'true' @? '$' AS changed`, true))
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonbPathOpsOne(`SELECT $1 @? $2 AS changed`, true, `true`, `$`))
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonbPathOpsOne(`SELECT $1 @@ $2::jsonpath AS changed`, false, `false`, `$`))
		for _, q := range []string{`SELECT 'cat' @@ 'cat' AS changed`, `SELECT 'cat'::text @@ 'cat' AS changed`} {
			if b.PG {
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonbPathOpsOne(q, true))
				continue
			}
			rows, err := c.QueryContext(p.Ctx, q)
			if rows != nil {
				rows.Close()
				t.Fatal("unsupported text search published rows")
			}
			fixture.TransactionOrdinary(t, err, "stuntgres: invalid comparison operator")
			stmt, err := c.PrepareContext(p.Ctx, q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("unsupported text search prepared")
			}
			fixture.TransactionOrdinary(t, err, "stuntgres: invalid comparison operator")
		}
		// Explicit path ordering masks only the matching unqualified signature.
		run(fixture.Exec(`CREATE OPERATOR public.@? (LEFTARG=jsonb,RIGHTARG=jsonpath,PROCEDURE=pg_catalog.jsonb_path_match_opr)`, 0))
		run(fixture.Exec(`SET search_path=public,pg_catalog`, 0))
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonbPathOpsOne(`SELECT 'false'::jsonb @? '$'::jsonpath AS changed`, false))
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonbPathOpsOne(`SELECT 'false'::jsonb OPERATOR(pg_catalog.@?) '$'::jsonpath AS changed`, true))
		run(fixture.Exec(`RESET search_path`, 0))
		run(fixture.Exec(`CREATE SCHEMA jpo_hidden`, 0))
		run(fixture.Exec(`CREATE USER jpo_bind_reader`, 0))
		defer func() {
			_, err := c.ExecContext(p.Ctx, `RESET SESSION AUTHORIZATION`)
			if err != nil {
				t.Error(err)
			}
		}()
		run(fixture.Exec(`SET SESSION AUTHORIZATION jpo_bind_reader`, 0))
		for _, op := range jsonbPathOps {
			jsonpathQueryBindError(t, p, b, c, `SELECT 'true'::jsonb OPERATOR(jpo_hidden.`+op+`) '$'::jsonpath`, "42501", `permission denied for schema jpo_hidden`, "OPERATOR", "")
		}
	})
}
