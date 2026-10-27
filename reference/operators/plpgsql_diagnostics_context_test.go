package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLDiagnosticsContext(t *testing.T) {
	diagnosticsDynamicContext(t)
	diagnosticsStaticContext(t)
	for _, site := range []string{"direct", "RETURN", "assignment", "initializer", "sql_noninline"} {
		t.Run(site, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION dg_leaf() RETURNS text LANGUAGE plpgsql AS $$DECLARE x text; BEGIN GET DIAGNOSTICS x=PG_CONTEXT; RETURN x; END$$")
				q, want := "SELECT dg_leaf() AS v", "PL/pgSQL function dg_leaf() line 1 at GET DIAGNOSTICS"
				body, action := "BEGIN RETURN dg_leaf(); END", "at RETURN"
				switch site {
				case "assignment":
					body, action = "DECLARE x text; BEGIN x:=dg_leaf(); RETURN x; END", "at assignment"
				case "initializer":
					body, action = "DECLARE x text:=dg_leaf(); BEGIN RETURN x; END", "during statement block local variable initialization"
				case "sql_noninline":
					userOperatorExec(t, p, b, c, "CREATE FUNCTION dg_sql() RETURNS text LANGUAGE SQL AS $$SELECT 'first'; SELECT dg_leaf()$$")
					body = "BEGIN RETURN dg_sql(); END"
					want += "\nSQL function \"dg_sql\" statement 2"
				}
				if site != "direct" {
					userOperatorExec(t, p, b, c, "CREATE FUNCTION dg_parent() RETURNS text LANGUAGE plpgsql AS $$"+body+"$$")
					q = "SELECT dg_parent() AS v"
					want += "\nPL/pgSQL function dg_parent() line 1 " + action
				}
				sqlExecutionValue(t, p, b, c, q, "TEXT", want)
				// Returned strings are detached from the popped live frames.
				var retained string
				if err := c.QueryRowContext(p.Ctx, q).Scan(&retained); err != nil {
					t.Fatal(err)
				}
				sqlExecutionValue(t, p, b, c, "SELECT dg_leaf() AS v", "TEXT", "PL/pgSQL function dg_leaf() line 1 at GET DIAGNOSTICS")
				if retained != want {
					t.Fatalf("retained context=%q want=%q", retained, want)
				}
			})
		})
	}
	t.Run("utf8_repeated_lines", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION dg_utf8() RETURNS text LANGUAGE plpgsql AS $$\nDECLARE \"é\" text; other text;\nBEGIN\n GET CURRENT DIAGNOSTICS \"é\"=PG_CONTEXT;\n GET DIAGNOSTICS other=PG_CONTEXT;\n RETURN \"é\" || '|' || other;\nEND$$")
			sqlExecutionValue(t, p, b, c, "SELECT dg_utf8() AS v", "TEXT", "PL/pgSQL function dg_utf8() line 4 at GET DIAGNOSTICS|PL/pgSQL function dg_utf8() line 5 at GET DIAGNOSTICS")
		})
	})
	t.Run("current_versus_caught", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION dg_context_handler() RETURNS text LANGUAGE plpgsql AS $$\nDECLARE a text; b text; c text;\nBEGIN\n RAISE EXCEPTION 'original';\nEXCEPTION WHEN OTHERS THEN\n GET STACKED DIAGNOSTICS a=PG_EXCEPTION_CONTEXT, b=PG_CONTEXT;\n GET CURRENT DIAGNOSTICS c=PG_CONTEXT;\n RETURN a || '|' || b || '|' || c;\nEND$$")
			sqlExecutionValue(t, p, b, c, "SELECT dg_context_handler() AS v", "TEXT", "PL/pgSQL function dg_context_handler() line 4 at RAISE|PL/pgSQL function dg_context_handler() line 6 at GET STACKED DIAGNOSTICS|PL/pgSQL function dg_context_handler() line 7 at GET DIAGNOSTICS")
		})
	})
	t.Run("do_identity_and_oid", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE dg_inline(context text, routine oid)")
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("DO $$DECLARE c text; o oid; BEGIN GET DIAGNOSTICS c=PG_CONTEXT,o=PG_ROUTINE_OID; INSERT INTO dg_inline VALUES(c,o); END$$", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				sqlExecutionValue(t, p, b, c, "SELECT context || ':' || routine::text AS v FROM dg_inline", "TEXT", "PL/pgSQL function inline_code_block line 1 at GET DIAGNOSTICS:0")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM dg_inline", 1))
			}
		})
	})
}
