package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineParametersNotationDefaults(t *testing.T) {
	parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION rp_add(a int,b int DEFAULT 7,c int DEFAULT 9) RETURNS int LANGUAGE SQL AS 'SELECT $1+$2+$3'",
			`CREATE FUNCTION rp_quote("First" int,"Last" int = 5) RETURNS int RETURN "First"+"Last"`)
		for _, tc := range []struct {
			q string
			v int64
		}{
			{"SELECT rp_add(1,2,3) AS v", 6},
			{"SELECT rp_add(1) AS v", 17},
			{"SELECT rp_add(c=>3,a=>1,b=>2) AS v", 6},
			{"SELECT rp_add(1,c=>3) AS v", 11},
			{"SELECT rp_add(a=>1,c=>3) AS v", 11},
			{"SELECT rp_add(c:=3,a:=1) AS v", 11},
			{`SELECT rp_quote("Last"=>3,"First"=>1) AS v`, 4},
		} {
			parametersValue(t, p, b, c, tc.q, "v", "INT4", tc.v)
		}
		q := "SELECT rp_add(c=>$2::int,a=>$1::int) AS v"
		parametersValue(t, p, b, c, q, "v", "INT4", int64(11), 1, 3)
		parametersHeld(t, p, b, c, q, "INT4", [][]any{{1, 3}, {2, 3}, {nil, 3}, {1, 3}}, []any{int64(11), int64(12), nil, int64(11)})
		// Declaration defaults are private recipes, not external placeholders.
		create, err := c.PrepareContext(p.Ctx, "CREATE FUNCTION rp_created(a int DEFAULT 6) RETURNS int LANGUAGE SQL AS 'SELECT $1'")
		if err != nil {
			t.Fatal(err)
		}
		result, err := create.ExecContext(p.Ctx)
		create.Close()
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 0 {
			t.Fatal(n, err)
		}
		parametersValue(t, p, b, c, "SELECT rp_created() AS v", "v", "INT4", int64(6))
	})
}

func TestReferenceRoutineParametersVariadic(t *testing.T) {
	parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION rp_array(VARIADIC xs integer[]) RETURNS integer[] LANGUAGE SQL AS 'SELECT $1'",
			"CREATE FUNCTION rp_empty(VARIADIC xs integer[] DEFAULT '{}'::integer[]) RETURNS integer[] LANGUAGE SQL AS 'SELECT $1'",
			"CREATE FUNCTION rp_plain(xs integer[]) RETURNS integer[] LANGUAGE SQL AS 'SELECT $1'")
		for _, tc := range []struct {
			q string
			v any
		}{
			{"SELECT rp_array(1,2,3) AS v", "{1,2,3}"},
			{"SELECT rp_array(1,NULL,3) AS v", "{1,NULL,3}"},
			{"SELECT rp_array(VARIADIC '{4,NULL,6}'::integer[]) AS v", "{4,NULL,6}"},
			{"SELECT rp_array(VARIADIC '{}'::integer[]) AS v", "{}"},
			{"SELECT rp_array(VARIADIC NULL::integer[]) AS v", nil},
			{"SELECT rp_array(VARIADIC xs=>'{4,6}'::integer[]) AS v", "{4,6}"},
			{"SELECT rp_array(VARIADIC '[-1:0]={4,NULL}'::integer[]) AS v", "[-1:0]={4,NULL}"},
			{"SELECT rp_array(VARIADIC '[0:1][3:4]={{1,NULL},{3,4}}'::integer[]) AS v", "[0:1][3:4]={{1,NULL},{3,4}}"},
			// Both release edges observed on pinned18.6 in tests-3.md.
			{"SELECT rp_empty() AS v", "{}"},
			{"SELECT rp_plain(VARIADIC '{8,9}'::integer[]) AS v", "{8,9}"},
		} {
			t.Run(tc.q, func(t *testing.T) { parametersValue(t, p, b, c, tc.q, "v", "_INT4", tc.v) })
		}
		parametersHeld(t, p, b, c, "SELECT rp_array(VARIADIC xs=>$1::integer[]) AS v", "_INT4",
			[][]any{{"{7,NULL}"}, {"{}"}, {nil}, {"[0:1]={8,9}"}}, []any{"{7,NULL}", "{}", nil, "[0:1]={8,9}"})
		parametersHeld(t, p, b, c, "SELECT rp_array($1::int,$2::int) AS v", "_INT4",
			[][]any{{1, 2}, {3, nil}, {nil, 4}}, []any{"{1,2}", "{3,NULL}", "{NULL,4}"})
		userOperatorExec(t, p, b, c, "CREATE FUNCTION rp_array(integer) RETURNS integer[] LANGUAGE SQL AS 'SELECT ARRAY[99]'")
		parametersValue(t, p, b, c, "SELECT rp_array(1) AS v", "v", "_INT4", "{99}")
		parametersValue(t, p, b, c, "SELECT rp_array(VARIADIC '{1}'::integer[]) AS v", "v", "_INT4", "{1}")
	})
}

func TestReferenceRoutineParametersScalarOutputsIdentity(t *testing.T) {
	parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION rp_out(IN x int,OUT y int) LANGUAGE SQL AS 'SELECT $1+1'",
			"CREATE FUNCTION rp_io(INOUT n int DEFAULT 4) LANGUAGE SQL AS 'SELECT n+1'",
			"CREATE FUNCTION rp_only(OUT y int) LANGUAGE SQL AS 'SELECT 7'",
			"CREATE FUNCTION rp_before(OUT y int,IN x int) LANGUAGE SQL AS 'SELECT $1+1'",
			"CREATE FUNCTION rp_same(IN x int,OUT x int) LANGUAGE SQL AS 'SELECT $1+1'",
			"CREATE FUNCTION rp_out_default(IN x int DEFAULT 4,OUT y int) RETURNS int LANGUAGE SQL AS 'SELECT $1+1'",
			"CREATE FUNCTION rp_out_variadic(VARIADIC xs int[],OUT y int[]) LANGUAGE SQL AS 'SELECT $1'")
		for _, q := range []string{"SELECT rp_out(4) AS v", "SELECT rp_out(x=>4) AS v", "SELECT rp_io(4) AS v", "SELECT rp_io() AS v", "SELECT rp_before(4) AS v", "SELECT rp_same(4) AS v", "SELECT rp_out_default() AS v"} {
			parametersValue(t, p, b, c, q, "v", "INT4", int64(5))
		}
		parametersValue(t, p, b, c, "SELECT rp_only() AS v", "v", "INT4", int64(7))
		parametersValue(t, p, b, c, "SELECT rp_out(4)", "rp_out", "INT4", int64(5))
		parametersValue(t, p, b, c, "SELECT * FROM rp_out(4)", "y", "INT4", int64(5))
		parametersValue(t, p, b, c, "SELECT * FROM rp_out(4) AS renamed", "y", "INT4", int64(5))
		parametersValue(t, p, b, c, "SELECT * FROM public.rp_out(x=>4)", "y", "INT4", int64(5))
		parametersValue(t, p, b, c, "SELECT z FROM rp_out(4) AS renamed(z)", "z", "INT4", int64(5))
		parametersValue(t, p, b, c, "SELECT * FROM rp_io()", "n", "INT4", int64(5))
		parametersValue(t, p, b, c, "SELECT * FROM rp_only()", "y", "INT4", int64(7))
		parametersValue(t, p, b, c, "SELECT rp_out_variadic(1,2) AS v", "v", "_INT4", "{1,2}")
		parametersHeld(t, p, b, c, "SELECT rp_io(n=>$1::int) AS v", "INT4", [][]any{{4}, {9}, {nil}, {4}}, []any{int64(5), int64(10), nil, int64(5)})
		parametersError(t, p, c, "CREATE FUNCTION rp_out(IN x int,OUT other bigint) LANGUAGE SQL AS 'SELECT $1::bigint'", false, "42723", `function "rp_out" already exists with same argument types`, "", "")
		parametersValue(t, p, b, c, "SELECT rp_out(4) AS v", "v", "INT4", int64(5))
		userOperatorValue(t, p, b, c, "SELECT 'rp_out(integer)'::regprocedure::oid = (SELECT oid FROM pg_proc WHERE proname='rp_out') AS v", "BOOL", true)
		userOperatorValue(t, p, b, c, "SELECT pronargs::integer AS v FROM pg_proc WHERE oid='rp_only()'::regprocedure", "INT4", int64(0))
		userOperatorValue(t, p, b, c, "SELECT pronargs::integer AS v FROM pg_proc WHERE oid='rp_out(integer)'::regprocedure", "INT4", int64(1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ArrayReferenceSelect("SELECT proargnames AS v FROM pg_proc WHERE oid='rp_before(integer)'::regprocedure", []string{"v"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{"{y,x}"}}, b.PG))
		userOperatorExec(t, p, b, c,
			"GRANT EXECUTE ON FUNCTION rp_out(integer) TO PUBLIC",
			"GRANT EXECUTE ON FUNCTION rp_out_variadic(integer[]) TO PUBLIC",
			"DROP FUNCTION rp_out(IN integer,OUT integer)",
			"DROP FUNCTION rp_out_variadic(VARIADIC integer[],OUT integer[])")
		userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rp_out' OR proname='rp_out_variadic'", "INT8", int64(0))
	})
}
