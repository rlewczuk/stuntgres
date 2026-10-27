package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGProcDeclarations(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			pgProcExec(t, p, b, b.DB,
				"CREATE FUNCTION pp_decl_over(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
				"CREATE FUNCTION pp_decl_over(text) RETURNS text LANGUAGE SQL AS 'SELECT $1'",
				"CREATE FUNCTION pp_decl_zero() RETURNS integer LANGUAGE SQL RETURN 7",
				"CREATE FUNCTION pp_decl_partial(integer,b text) RETURNS text LANGUAGE SQL AS 'SELECT $2'",
				"CREATE FUNCTION pp_decl_io(IN a integer,OUT b text,INOUT c integer) LANGUAGE SQL AS $$SELECT a::text,c$$",
				"CREATE FUNCTION pp_decl_table(a integer) RETURNS TABLE(n integer,s text) LANGUAGE SQL AS $$SELECT a,'x'::text$$",
				"CREATE FUNCTION pp_decl_var(VARIADIC a integer[]) RETURNS integer LANGUAGE SQL AS 'SELECT cardinality(a)'",
				"CREATE FUNCTION pp_decl_default(a integer DEFAULT 7,OUT result integer,b text DEFAULT 'x') LANGUAGE SQL AS 'SELECT a'",
				"CREATE PROCEDURE pp_decl_proc() LANGUAGE SQL AS 'SELECT 1'",
				"CREATE PROCEDURE pp_decl_out(OUT answer integer) LANGUAGE SQL AS 'SELECT 7'",
				`CREATE FUNCTION "pp_Decl_Mixed"(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`)
			q := "SELECT proname,proargtypes,pronargs,pronargdefaults,prorettype,prokind,provariadic,proallargtypes,proargmodes,proargnames FROM pg_proc WHERE proname LIKE 'pp_decl_%' OR proname='pp_Decl_Mixed' ORDER BY proname,proargtypes"
			pgProcBoth(t, p, b, b.DB, q,
				[]string{"proname", "proargtypes", "pronargs", "pronargdefaults", "prorettype", "prokind", "provariadic", "proallargtypes", "proargmodes", "proargnames"},
				[]string{"NAME", "OIDVECTOR", "INT2", "INT2", "OID", "CHAR", "OID", "_OID", "_CHAR", "_TEXT"},
				[][]any{
					{"pp_Decl_Mixed", "23", int64(1), int64(0), int64(23), "f", int64(0), nil, nil, nil},
					{"pp_decl_default", "23 25", int64(2), int64(2), int64(23), "f", int64(0), "{23,23,25}", "{i,o,i}", "{a,result,b}"},
					{"pp_decl_io", "23 23", int64(2), int64(0), int64(2249), "f", int64(0), "{23,25,23}", "{i,o,b}", "{a,b,c}"},
					{"pp_decl_out", "", int64(0), int64(0), int64(2249), "p", int64(0), "{23}", "{o}", "{answer}"},
					{"pp_decl_over", "23", int64(1), int64(0), int64(23), "f", int64(0), nil, nil, nil},
					{"pp_decl_over", "25", int64(1), int64(0), int64(25), "f", int64(0), nil, nil, nil},
					{"pp_decl_partial", "23 25", int64(2), int64(0), int64(25), "f", int64(0), nil, nil, "{\"\",b}"},
					{"pp_decl_proc", "", int64(0), int64(0), int64(2278), "p", int64(0), nil, nil, nil},
					{"pp_decl_table", "23", int64(1), int64(0), int64(2249), "f", int64(0), "{23,23,25}", "{i,t,t}", "{a,n,s}"},
					{"pp_decl_var", "1007", int64(1), int64(0), int64(23), "f", int64(23), "{1007}", "{v}", "{a}"},
					{"pp_decl_zero", "", int64(0), int64(0), int64(23), "f", int64(0), nil, nil, nil},
				})
			pgProcBoth(t, p, b, b.DB, "SELECT array_lower(proargtypes,1) AS low,cardinality(proargtypes) AS n FROM pg_proc WHERE proname='pp_decl_zero'", []string{"low", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(0)}})
			pgProcBoth(t, p, b, b.DB, "SELECT array_lower(proallargtypes,1) AS a,array_lower(proargmodes,1) AS m,array_lower(proargnames,1) AS n FROM pg_proc WHERE proname='pp_decl_io'", []string{"a", "m", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1)}})
			pgProcBoth(t, p, b, b.DB, "SELECT pp_decl_over(7) AS i,pp_decl_over('x'::text) AS s,pp_decl_zero() AS z,pp_decl_default() AS d,pp_decl_var(1,2) AS v", []string{"i", "s", "z", "d", "v"}, []string{"INT4", "TEXT", "INT4", "INT4", "INT4"}, [][]any{{int64(7), "x", int64(7), int64(7), int64(2)}})
			pgProcBoth(t, p, b, b.DB, "SELECT * FROM pp_decl_io(7,9)", []string{"b", "c"}, []string{"TEXT", "INT4"}, [][]any{{"7", int64(9)}})
			pgProcBoth(t, p, b, b.DB, "SELECT * FROM pp_decl_table(7)", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "x"}})
			pgProcBoth(t, p, b, b.DB, "CALL pp_decl_out(NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(7)}})
		})
	}
}
