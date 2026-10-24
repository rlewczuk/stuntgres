package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLQueryLoopsControl(t *testing.T) {
	t.Run("found_and_transfers", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION ql_found() RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; trace text:=''; BEGIN FOR r IN SELECT g AS i FROM generate_series(1,3) AS g ORDER BY g LOOP trace:=trace||FOUND||';'; IF r.i=1 THEN FOUND:=true; ELSE FOUND:=false; END IF; END LOOP; trace:=trace||FOUND||';'; FOR r IN SELECT 1 AS i WHERE false LOOP trace:='bad'; END LOOP; RETURN trace||FOUND; END$$",
				"CREATE FUNCTION ql_transfers() RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; s record; n integer:=0; trace text:=''; BEGIN <<outer_loop>> FOR r IN SELECT g AS i FROM generate_series(1,4) AS g ORDER BY g LOOP <<inner_loop>> FOR s IN SELECT g AS j FROM generate_series(1,3) AS g ORDER BY g LOOP CONTINUE inner_loop WHEN s.j=1; CONTINUE outer_loop WHEN r.i=2; EXIT outer_loop WHEN r.i=3; n:=n+1; trace:=trace||r.i||':'||s.j||';'; END LOOP inner_loop; END LOOP outer_loop; RETURN n||'|'||trace||'|'||r.i||':'||s.j||'|'||FOUND; END$$",
				"CREATE FUNCTION ql_exit() RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; BEGIN FOR r IN SELECT g AS i FROM generate_series(1,4) AS g ORDER BY g LOOP EXIT WHEN r.i=2; END LOOP; RETURN r.i||':'||FOUND; END$$",
				"CREATE FUNCTION ql_return() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; BEGIN FOR r IN SELECT g AS i FROM generate_series(1,4) AS g ORDER BY g LOOP IF r.i=2 THEN RETURN r.i; END IF; END LOOP; RETURN 99; END$$")
			plpgsqlValue(t, p, b, c, "SELECT ql_found() AS v", "TEXT", "false;true;false;true;false")
			plpgsqlValue(t, p, b, c, "SELECT ql_transfers() AS v", "TEXT", "2|1:2;1:3;|3:2|true")
			plpgsqlValue(t, p, b, c, "SELECT ql_exit() AS v", "TEXT", "2:true")
			plpgsqlValue(t, p, b, c, "SELECT ql_return() AS v", "INT4", int64(2))
		})
	})
	// The harmless CTE contains 100 rows; only the OUTER projection of row 100
	// fails. Do not add ORDER BY (which could force that projection eagerly),
	// constant 1/0 (which could fold), or a first-value/order assertion.
	// EXIT/RETURN candidates are reasoned from bounded portal demand; pinned
	// PostgreSQL discovery is the authority for the source-owner design gate.
	for _, tc := range []struct {
		name, tail string
		want       int64
		fails      bool
	}{
		{"cte_early_exit", "n:=n+1; EXIT;", 1, false},
		{"cte_early_return", "RETURN 7;", 7, false},
		{"cte_full_consume", "n:=n+1;", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION ql_cte_demand() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; n integer:=0; BEGIN FOR r IN WITH q AS (SELECT g AS i FROM generate_series(1,100) AS g) SELECT 100/(100-i) AS v FROM q LOOP "+tc.tail+" END LOOP; RETURN n; END$$")
				q := "SELECT ql_cte_demand() AS v"
				if tc.fails {
					want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "ql_cte_demand()", "FOR over SELECT rows")
					plpgsqlError(t, p, c, q, true, want)
				} else {
					plpgsqlValue(t, p, b, c, q, "INT4", tc.want)
				}
				plpgsqlValue(t, p, b, c, "SELECT 1 AS v", "INT4", int64(1))
			})
		})
	}
	t.Run("cte_nested_query_resume", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION ql_cte_nested() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; s record; n integer:=0; BEGIN FOR r IN WITH q AS (SELECT g AS i FROM generate_series(1,100) AS g) SELECT i FROM q ORDER BY i LOOP FOR s IN WITH q AS (SELECT r.i AS j) SELECT j FROM q LOOP n:=n+s.j; END LOOP; END LOOP; RETURN n; END$$")
			plpgsqlValue(t, p, b, c, "SELECT ql_cte_nested() AS v", "INT4", int64(5050))
		})
	})
}
