package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgTrgmDiagnostics(t *testing.T) {
	t.Run("LookupAndDecorations", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			fail := func(s fixture.Step) {
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
				s.Prepared, s.ErrorStage = true, "prepare"
				run(s)
			}
			for _, d := range pgTrgmDeclarations {
				args, types := "'cat'", "unknown"
				if d.arity == 2 {
					args += ",'cat'"
					types += ", unknown"
				}
				if d.inputs == "real" {
					args, types = "0.25::real", "real"
				}
				if d.arity == 0 {
					args, types = "", ""
				}
				fail(extensionMissing("SELECT "+d.name+"("+args+")", d.name, types))
			}
			run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
			for _, d := range pgTrgmDeclarations {
				if d.arity == 0 {
					fail(extensionMissing("SELECT "+d.name+"(1)", d.name, "integer"))
				} else {
					fail(extensionMissing("SELECT "+d.name+"()", d.name, ""))
				}
				args, types := "1", "integer"
				if d.arity == 2 {
					args += ",'cat'"
					types += ", unknown"
				}
				if d.inputs == "real" || d.arity == 0 {
					args, types = "true", "boolean"
				}
				fail(extensionMissing("SELECT "+d.name+"("+args+")", d.name, types))
				named, signature := "value => 'cat'", "value => unknown"
				if d.arity == 2 {
					named += " ,target => 'cat'"
					signature += ", target => unknown"
				}
				if d.inputs == "real" {
					named, signature = "value => 0.25::real", "value => real"
				}
				fail(extensionMissing("SELECT "+d.name+"("+named+")", d.name, signature))
				args = "'cat'"
				if d.arity == 2 {
					args += ",'cat'"
				}
				if d.inputs == "real" {
					args = "0.25::real"
				}
				if d.arity == 0 {
					args = ""
				}
				decorations := []struct{ suffix, decoration, kind string }{
					{"(" + args + ") FILTER (WHERE true)", "FILTER", "an aggregate function"},
					{"(" + args + ") OVER ()", "OVER", "a window function nor an aggregate function"},
				}
				if d.arity != 0 {
					decorations = append(decorations,
						struct{ suffix, decoration, kind string }{"(DISTINCT " + args + ")", "DISTINCT", "an aggregate function"},
						struct{ suffix, decoration, kind string }{"(" + args + " ORDER BY 'cat')", "ORDER BY", "an aggregate function"},
					)
				}
				for _, tc := range decorations {
					fail(pgTrgmFailure("SELECT "+d.name+tc.suffix, fixture.Diagnostic{
						Code: "42809", Message: tc.decoration + " specified, but " + d.name + " is not " + tc.kind, Position: "8",
					}))
				}
			}
			for _, tc := range []struct{ q, name, types string }{
				{"SELECT similarity('cat')", "similarity", "unknown"},
				{"SELECT show_trgm('cat','dog')", "show_trgm", "unknown, unknown"},
				{`SELECT "Similarity"('cat','cat')`, "Similarity", "unknown, unknown"},
				{"SELECT similarity('cat',1)", "similarity", "unknown, integer"},
				{"SELECT similarity(true,'cat')", "similarity", "boolean, unknown"},
				{"SELECT similarity(ARRAY['cat'],'cat')", "similarity", "text[], unknown"},
			} {
				fail(extensionMissing(tc.q, tc.name, tc.types))
			}
			fail(pgTrgmFailure("SELECT trgm_absent.similarity('cat','cat')", fixture.Diagnostic{Code: "3F000", Message: `schema "trgm_absent" does not exist`, Position: "8"}))
			q := "SELECT similarity(source => 'cat','cat')"
			fail(pgTrgmFailure(q, fixture.Diagnostic{
				Code: "42601", Message: "positional argument cannot follow named argument", Position: fixture.Position(strings.LastIndex(q, "'cat'") + 1),
			}))
			// Diagnostic positions count characters, not UTF8 bytes.
			q = "SELECT 'é', similarity(1,'cat')"
			fail(pgTrgmFailure(q, fixture.Diagnostic{
				Code: "42883", Message: "function similarity(integer, unknown) does not exist", Position: "13",
				Hint: "No function matches the given name and argument types. You might need to add explicit type casts.",
			}))
			for _, op := range pgTrgmOperators {
				q := "SELECT 1 " + op.token + " 'cat'::text"
				fail(pgTrgmFailure(q, fixture.Diagnostic{
					Code: "42883", Message: "operator does not exist: integer " + op.token + " text", Position: "10",
					Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.",
				}))
			}
			run(fixture.Exec("SET search_path=pg_catalog", 0))
			fail(extensionMissing("SELECT similarity('cat','cat')", "similarity", "unknown, unknown"))
			run(pgTrgmScalar(b, "SELECT public.similarity('cat','cat')", "similarity", "FLOAT4", float64(1)))
			run(fixture.Exec("RESET search_path", 0))
			pgTrgmNotices(t, notices)
		})
	})
	for _, setting := range pgTrgmSettings {
		t.Run("ThresholdInputs/"+setting.name, func(t *testing.T) {
			pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				run := pgTrgmRun(t, p, b, c)
				run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
				for _, tc := range []struct{ input, message string }{
					{"2", fmt.Sprintf("2 is outside the valid range for parameter %q (0 .. 1)", setting.name)},
					{"-0.01", fmt.Sprintf("-0.01 is outside the valid range for parameter %q (0 .. 1)", setting.name)},
					{"1.000000001", fmt.Sprintf("1 is outside the valid range for parameter %q (0 .. 1)", setting.name)},
					{"Infinity", fmt.Sprintf("Infinity is outside the valid range for parameter %q (0 .. 1)", setting.name)},
					{"-Infinity", fmt.Sprintf("-Infinity is outside the valid range for parameter %q (0 .. 1)", setting.name)},
					{"NaN", ""}, {"bad", ""}, {"1kB", ""}, {"0.5s", ""}, {"", ""}, {"0.5junk", ""}, {"1e999", ""},
				} {
					d := pgTrgmBadSetting(setting.name, tc.input)
					if tc.message != "" {
						d.Message = tc.message
					}
					run(fixture.ImplicitError("SET "+setting.name+"="+pgTrgmLiteral(tc.input), d))
					pgTrgmSettingValue(run, setting.name, setting.boot)
					for _, prepared := range []bool{false, true} {
						s := pgTrgmFailure("SELECT set_config($1,$2,false)", d, setting.name, tc.input)
						s.Prepared = prepared
						run(s)
						pgTrgmSettingValue(run, setting.name, setting.boot)
					}
				}
				for _, input := range []string{"0", "1"} {
					run(fixture.Exec("SET "+setting.name+"="+input, 0))
					pgTrgmSettingValue(run, setting.name, input)
				}
				run(fixture.Exec("BEGIN", 0))
				run(fixture.ImplicitError("SET "+setting.name+"='bad'", pgTrgmBadSetting(setting.name, "bad")))
				run(fixture.Exec("ROLLBACK", 0))
				pgTrgmSettingValue(run, setting.name, "1")
				pgTrgmNotices(t, notices)
			})
		})
	}
	t.Run("ExecuteAuthorityBeforeArgumentDemand", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			pgTrgmInstalledElsewhere(t, p, b)
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("CREATE USER trgm_reader NOSUPERUSER", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO trgm_reader", 0))
			run(fixture.Exec("CREATE TABLE trgm_authority(v text,n int)", 0))
			run(fixture.Exec("INSERT INTO trgm_authority VALUES(NULL,0)", 1))
			run(fixture.Exec("GRANT SELECT ON trgm_authority TO trgm_reader", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION similarity(text,text) FROM PUBLIC", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION trgm_reader", 0))
			q := "SELECT similarity(v,(1/n)::text) FROM trgm_authority"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			d := fixture.Diagnostic{Code: "42501", Message: "permission denied for function similarity"}
			run(pgTrgmFailure(q, d))
			run(fixture.Prepared(pgTrgmFailure(q, d)))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmFailure(q, d))
			pgTrgmLoaded(run, false)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION similarity(text,text) TO trgm_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION trgm_reader", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmFailure(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			pgTrgmLoaded(run, true)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION similarity(text,text) FROM trgm_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION trgm_reader", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmFailure(q, d))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			pgTrgmNotices(t, notices)
		})
	})
	t.Run("SchemaAuthority", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("CREATE SCHEMA trgm_addon", 0))
			run(fixture.Exec("CREATE EXTENSION pg_trgm SCHEMA trgm_addon", 0))
			run(fixture.Exec("CREATE USER trgm_reader NOSUPERUSER", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION trgm_reader", 0))
			s := pgTrgmFailure("SELECT trgm_addon.similarity('cat','cat')", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema trgm_addon", Position: "8"})
			run(s)
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			pgTrgmNotices(t, notices)
		})
	})
}
