package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBExistsDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"jbe_reader"})
	fixture.SchemaReferenceCleanup(t, "jbe_private")
	fixture.SchemaReferenceCleanup(t, "jbe_empty")
	jsonbExistsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q    string
			pos  int
			args []any
		}{
			{`SELECT $1 ?| $2`, 11, []any{`{"a":1}`, "{a}"}},
			{`SELECT '{"a":1}' ?| '{a}' AS v`, 18, nil},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42725", "operator is not unique: unknown ?| unknown")
				d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
				d.Position = fixture.Position(tc.pos)
				s := fixture.ErrorDiagnostic(tc.q, d, tc.args...)
				s.ErrorStage = "execute"
				for _, selectRows := range []bool{true, false} {
					s.SelectRows = selectRows
					jsonbExistsBoth(t, p, b, c, s)
				}
			})
		}
		for _, tc := range []struct {
			q, signature string
			pos          int
		}{
			{`SELECT '{}'::json ? 'a'`, "json ? unknown", 19},
			{`SELECT '{}'::jsonb ? 1`, "jsonb ? integer", 20},
			{`SELECT '{}'::jsonb ?| ARRAY[1]`, "jsonb ?| integer[]", 20},
			{`SELECT '{}'::jsonb ?& 'a'::text`, "jsonb ?& text", 20},
			{`SELECT /*é*/ '{}'::jsonb ? 1`, "jsonb ? integer", 26},
			{`SELECT '{}'::text ? 'a'`, "text ? unknown", 19},
			{`SELECT NULL::jsonb ? 1 WHERE FALSE`, "jsonb ? integer", 20},
			{`SELECT '{}'::jsonb OPERATOR(pg_catalog.?) 1`, "jsonb pg_catalog.? integer", 20},
			{`SELECT '{}'::jsonb OPERATOR(jbe_empty.?) 'a'`, "jsonb jbe_empty.? unknown", 20},
		} {
			if tc.signature == "jsonb jbe_empty.? unknown" {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA jbe_empty", 0))
			}
			t.Run(tc.q, func(t *testing.T) {
				s := jsonbExistsSignature(tc.q, tc.signature, tc.pos)
				for _, selectRows := range []bool{true, false} {
					s.SelectRows = selectRows
					jsonbExistsBoth(t, p, b, c, s)
				}
			})
		}
		t.Run("unary", func(t *testing.T) {
			d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: ? jsonb")
			d.Hint = "No operator matches the given name and argument type. You might need to add an explicit type cast."
			d.Position = "8"
			s := fixture.ErrorDiagnostic(`SELECT ? '{}'::jsonb`, d)
			s.SelectRows = true
			jsonbExistsBoth(t, p, b, c, s)
		})
		t.Run("missing namespace", func(t *testing.T) {
			d := fixture.ControlsReferenceDiagnostic("3F000", `schema "jbe_missing" does not exist`)
			d.Position = "20"
			s := fixture.ErrorDiagnostic(`SELECT '{}'::jsonb OPERATOR(jbe_missing.?) 'a'`, d)
			s.SelectRows = true
			jsonbExistsBoth(t, p, b, c, s)
		})
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE USER jbe_reader", 0))
		run(fixture.Exec("CREATE SCHEMA jbe_private", 0))
		run(fixture.Exec("CREATE TABLE jbe_acl(doc jsonb,key text,keys text[])", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO jbe_reader", 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		t.Run("namespace authorization", func(t *testing.T) {
			run(fixture.Exec("SET SESSION AUTHORIZATION jbe_reader", 0))
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema jbe_private")
			d.Position = "20"
			s := fixture.ErrorDiagnostic(`SELECT NULL::jsonb OPERATOR(jbe_private.?&) '{}'::text[]`, d)
			s.SelectRows = true
			jsonbExistsBoth(t, p, b, c, s)
		})
		deny := func(q string) {
			t.Run(q, func(t *testing.T) {
				s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jbe_acl"))
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				s = fixture.Prepared(s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
		run(fixture.Exec("GRANT SELECT(key,keys) ON jbe_acl TO jbe_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION jbe_reader", 0))
		for _, q := range []string{"SELECT doc ? key FROM jbe_acl", "SELECT doc ? NULL::text FROM jbe_acl WHERE FALSE", "SELECT doc ?& '{}'::text[] FROM jbe_acl"} {
			deny(q)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(key,keys) ON jbe_acl FROM jbe_reader", 0))
		run(fixture.Exec("GRANT SELECT(doc) ON jbe_acl TO jbe_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION jbe_reader", 0))
		for _, q := range []string{"SELECT doc ? key FROM jbe_acl", "SELECT NULL::jsonb ? key FROM jbe_acl WHERE FALSE", "SELECT NULL::jsonb ?| keys FROM jbe_acl"} {
			deny(q)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec(`INSERT INTO jbe_acl VALUES('{"a":1}','a','{a}')`, 1))
		run(fixture.Exec("GRANT SELECT(key) ON jbe_acl TO jbe_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION jbe_reader", 0))
		jsonbExistsBoth(t, p, b, c, jsonbExistsSelect("SELECT doc ? key AS v FROM jbe_acl", true))
		deny("SELECT doc ?& keys FROM jbe_acl")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
