package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCoreBlocks(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		value      int64
	}{
		{"ordered", "DECLARE x integer := 1; y integer DEFAULT x+1; BEGIN RETURN y; END", 2},
		{"shadow", "<<outer_block>> DECLARE x integer := 10; BEGIN DECLARE x integer := 20; BEGIN x = x+1; END; RETURN outer_block.x; END outer_block", 10},
		{"early_nested", "BEGIN BEGIN RETURN 7; END; RETURN 8; END", 7},
		{"quoted", "DECLARE s text := 'END; --'; BEGIN /* ; END */ RETURN length(s); END", 7},
		{"local_alias", "DECLARE x integer := 1; a ALIAS FOR x; BEGIN a := 9; RETURN x; END", 9},
		{"scope_pop", "DECLARE x integer := 10; BEGIN DECLARE x integer := 20; BEGIN x := 21; END; RETURN x; END", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION pl_block() RETURNS integer LANGUAGE plpgsql AS $body$"+tc.body+"$body$")
				plpgsqlValue(t, p, b, c, "SELECT pl_block() AS v", "INT4", tc.value)
			})
		})
	}
	t.Run("inline", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"DO $$DECLARE x integer := 1; BEGIN x := x+1; END$$",
				"DO LANGUAGE plpgsql $$BEGIN BEGIN END; RETURN; END$$",
				"DO $$BEGIN END$$ LANGUAGE plpgsql",
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(q, 0)))
			}
			plpgsqlError(t, p, c, "DO LANGUAGE sql $$SELECT 1$$", false, fixture.ControlsReferenceDiagnostic("0A000", `language "sql" does not support inline code execution`))
			plpgsqlError(t, p, c, "DO LANGUAGE pl_core_missing $$BEGIN END$$", false, fixture.ControlsReferenceDiagnostic("42704", `language "pl_core_missing" does not exist`))
		})
	})
}

func TestReferencePLpgSQLCoreParameters(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION pl_alias(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE a ALIAS FOR $1; n integer := a; BEGIN a := n+1; RETURN $1; END$$",
			"CREATE FUNCTION pl_out(OUT y integer, IN x integer) LANGUAGE plpgsql AS $$DECLARE a ALIAS FOR $1; BEGIN a := $2+1; RETURN; END$$",
			"CREATE FUNCTION sql_out(OUT y integer, IN x integer) LANGUAGE SQL AS 'SELECT $1+1'",
			"CREATE FUNCTION pl_inout(INOUT x integer) LANGUAGE plpgsql AS $$BEGIN x := x+1; END$$",
			"CREATE FUNCTION pl_qualified(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer := 20; BEGIN RETURN pl_qualified.x; END$$")
		plpgsqlValue(t, p, b, c, "SELECT pl_alias(x=>4) AS v", "INT4", int64(5))
		parametersHeld(t, p, b, c, "SELECT pl_alias($1) AS v", "INT4", [][]any{{4}, {9}, {nil}, {4}}, []any{int64(5), int64(10), nil, int64(5)})
		plpgsqlValue(t, p, b, c, "SELECT pl_out(x=>4) AS v", "INT4", int64(5))
		plpgsqlValue(t, p, b, c, "SELECT sql_out(x=>4) AS v", "INT4", int64(5))
		plpgsqlValue(t, p, b, c, "SELECT pl_inout(4) AS v", "INT4", int64(5))
		plpgsqlValue(t, p, b, c, "SELECT pl_qualified(4) AS v", "INT4", int64(4))
		rows, err := c.QueryContext(p.Ctx, "SELECT pl_alias(4) AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		other, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		plpgsqlValue(t, p, b, other, "SELECT pl_alias(9) AS v", "INT4", int64(10))
		if !rows.Next() {
			t.Fatal("retained PL value lost", rows.Err())
		}
		var v int64
		if err := rows.Scan(&v); err != nil || v != 5 {
			t.Fatal(v, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("retained PL result cardinality", rows.Err())
		}
	})
}
