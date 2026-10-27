package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func diagnosticsDynamicContext(t *testing.T) {
	const leaf = "PL/pgSQL function dg_dynamic_leaf() line 1 at GET DIAGNOSTICS"
	for _, tc := range []struct{ name, body, identity, want string }{
		{"dynamic_execute", "\nDECLARE s text;\nBEGIN\n EXECUTE 'SELECT dg_dynamic_leaf()' INTO s;\n RETURN s;\nEND", "dg_dynamic_parent", leaf + "\nSQL statement \"SELECT dg_dynamic_leaf()\"\nPL/pgSQL function dg_dynamic_parent() line 4 at EXECUTE"},
		{"dynamic_command_expression", "\nDECLARE s text;\nBEGIN\n EXECUTE dg_dynamic_command() INTO s;\n RETURN s;\nEND", "dg_dynamic_parent", leaf + "\nPL/pgSQL function dg_dynamic_command() line 1 at RETURN\nPL/pgSQL function dg_dynamic_parent() line 4 at EXECUTE"},
		{"dynamic_using_expression", "\nDECLARE s text;\nBEGIN\n EXECUTE 'SELECT $1::text' INTO s USING dg_dynamic_leaf();\n RETURN s;\nEND", "dg_dynamic_parent", leaf + "\nPL/pgSQL function dg_dynamic_parent() line 4 at EXECUTE"},
		{"dynamic_return_query", "\nBEGIN\n RETURN QUERY EXECUTE 'SELECT dg_dynamic_leaf()';\nEND", "dg_dynamic_set", leaf + "\nSQL statement \"SELECT dg_dynamic_leaf()\"\nPL/pgSQL function dg_dynamic_set() line 3 at RETURN QUERY"},
		{"dynamic_return_query_command", "\nBEGIN\n RETURN QUERY EXECUTE dg_dynamic_command();\nEND", "dg_dynamic_set", leaf + "\nPL/pgSQL function dg_dynamic_command() line 1 at RETURN\nPL/pgSQL function dg_dynamic_set() line 3 at RETURN QUERY"},
		{"dynamic_return_query_using", "\nBEGIN\n RETURN QUERY EXECUTE 'SELECT $1::text' USING dg_dynamic_leaf();\nEND", "dg_dynamic_set", leaf + "\nPL/pgSQL function dg_dynamic_set() line 3 at RETURN QUERY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				result := "text"
				if tc.identity == "dg_dynamic_set" { result = "SETOF text" }
				userOperatorExec(t, p, b, c,
					"CREATE FUNCTION dg_dynamic_leaf() RETURNS text LANGUAGE plpgsql AS $$DECLARE s text; BEGIN GET DIAGNOSTICS s=PG_CONTEXT; RETURN s; END$$",
					"CREATE FUNCTION dg_dynamic_command() RETURNS text LANGUAGE plpgsql AS $$BEGIN RETURN format('SELECT %L::text',dg_dynamic_leaf()); END$$",
					"CREATE FUNCTION " + tc.identity + "() RETURNS " + result + " LANGUAGE plpgsql AS $$" + tc.body + "$$")
				q := "SELECT " + tc.identity + "() AS v"
				sqlExecutionValue(t, p, b, c, q, "TEXT", tc.want)
				var retained string
				if err := c.QueryRowContext(p.Ctx, q).Scan(&retained); err != nil { t.Fatal(err) }
				sqlExecutionValue(t, p, b, c, "SELECT dg_dynamic_leaf() AS v", "TEXT", leaf)
				sqlExecutionValue(t, p, b, c, q, "TEXT", tc.want)
				if retained != tc.want { t.Fatalf("retained dynamic context=%q want=%q", retained, tc.want) }
				if tc.name == "dynamic_execute" {
					userOperatorExec(t, p, b, c,
						"CREATE FUNCTION dg_dynamic_sql() RETURNS text LANGUAGE SQL AS $$SELECT 'first'; SELECT dg_dynamic_parent()$$",
						"CREATE FUNCTION dg_dynamic_outer() RETURNS text LANGUAGE plpgsql AS $$BEGIN RETURN dg_dynamic_sql(); END$$")
					sqlExecutionValue(t, p, b, c, "SELECT dg_dynamic_outer() AS v", "TEXT", tc.want + "\nSQL function \"dg_dynamic_sql\" statement 2\nPL/pgSQL function dg_dynamic_outer() line 1 at RETURN")
				}
			})
		})
	}
	t.Run("dynamic_handler_origin_restoration", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION dg_dynamic_leaf() RETURNS text LANGUAGE plpgsql AS $$DECLARE s text; BEGIN GET DIAGNOSTICS s=PG_CONTEXT; RETURN s; END$$",
				"CREATE FUNCTION dg_dynamic_handler() RETURNS text LANGUAGE plpgsql AS $$\nDECLARE a text; b text; c text; d text;\nBEGIN\n BEGIN\n  EXECUTE 'SELECT 1/0';\n EXCEPTION WHEN division_by_zero THEN\n  GET STACKED DIAGNOSTICS a=PG_EXCEPTION_CONTEXT,b=PG_CONTEXT;\n  GET DIAGNOSTICS c=PG_CONTEXT;\n END;\n d:=dg_dynamic_leaf();\n RETURN a || '|' || b || '|' || c || '|' || d;\nEND$$")
			sqlExecutionValue(t, p, b, c, "SELECT dg_dynamic_handler() AS v", "TEXT", "SQL statement \"SELECT 1/0\"\nPL/pgSQL function dg_dynamic_handler() line 5 at EXECUTE|PL/pgSQL function dg_dynamic_handler() line 7 at GET STACKED DIAGNOSTICS|PL/pgSQL function dg_dynamic_handler() line 8 at GET DIAGNOSTICS|" + leaf + "\nPL/pgSQL function dg_dynamic_handler() line 10 at assignment")
		})
	})
}
