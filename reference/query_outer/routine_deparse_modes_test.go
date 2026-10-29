package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const routineDeparseProcedure = "CREATE OR REPLACE PROCEDURE public.rd_p(IN x integer DEFAULT 7)\n LANGUAGE sql\nAS $procedure$SELECT x$procedure$\n"
const routineDeparseReturn = "CREATE OR REPLACE FUNCTION public.rd_return()\n RETURNS integer\n LANGUAGE sql\nRETURN 7\n"
const routineDeparseAtomic = "CREATE OR REPLACE FUNCTION public.rd_atomic()\n RETURNS integer\n LANGUAGE sql\nBEGIN ATOMIC\n SELECT 7;\n RETURN 8;\nEND\n"

func routineDeparseModesSeed(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	resultsExec(t,p,b,c,
		"CREATE PROCEDURE rd_p(IN x integer DEFAULT 7) LANGUAGE SQL AS 'SELECT x'",
		"CREATE PROCEDURE rd_inout(INOUT x integer DEFAULT 7) LANGUAGE SQL AS 'SELECT x'",
		"CREATE FUNCTION rd_mix(OUT first integer,IN x integer,INOUT total bigint,OUT note text,IN bonus integer DEFAULT 2) LANGUAGE SQL AS $$SELECT x,total+bonus,'ok'::text$$",
		"CREATE FUNCTION rd_table(x integer) RETURNS TABLE(a integer,b text) LANGUAGE SQL AS $$SELECT x,'row'::text$$",
		"CREATE FUNCTION rd_set(x integer) RETURNS SETOF integer LANGUAGE SQL AS 'SELECT x UNION ALL SELECT x'",
		"CREATE FUNCTION rd_var(VARIADIC xs integer[]) RETURNS integer[] LANGUAGE SQL AS 'SELECT xs'",
		"CREATE DOMAIN rd_domain AS integer",
		"CREATE TYPE rd_pair AS (a integer,b text)",
		"CREATE FUNCTION rd_domain_result(x rd_domain[]) RETURNS rd_domain[] LANGUAGE SQL AS 'SELECT x'",
		"CREATE FUNCTION rd_pair_result(x rd_pair) RETURNS rd_pair LANGUAGE SQL AS 'SELECT x'",
		"CREATE FUNCTION rd_poly(x anyelement) RETURNS anyelement LANGUAGE SQL AS 'SELECT x'",
		"CREATE FUNCTION rd_void() RETURNS void LANGUAGE SQL AS 'SELECT 1'",
		"CREATE FUNCTION rd_pl(a integer DEFAULT 7) RETURNS integer LANGUAGE plpgsql AS 'BEGIN RETURN a; END'",
		"CREATE FUNCTION rd_return() RETURNS integer RETURN 7",
		"CREATE FUNCTION rd_atomic() RETURNS integer BEGIN ATOMIC SELECT 7; RETURN 8; END")
	resultsExec(t,p,b,c,
		"CREATE FUNCTION rd_leaf() RETURNS integer LANGUAGE SQL AS 'SELECT 4'",
		"CREATE FUNCTION rd_frozen(a integer DEFAULT 1) RETURNS integer RETURN rd_leaf()+a",
		"ALTER FUNCTION rd_leaf() RENAME TO rd_leaf_renamed",
		"CREATE FUNCTION rd_leaf() RETURNS integer LANGUAGE SQL AS 'SELECT 9'")
}

func routineDeparseModesCheck(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	routineDeparseFour(t,p,b,c,"rd_p(integer)",[4]any{routineDeparseProcedure,"IN x integer DEFAULT 7","IN x integer",nil})
	routineDeparseFour(t,p,b,c,"rd_inout(integer)",[4]any{"CREATE OR REPLACE PROCEDURE public.rd_inout(INOUT x integer DEFAULT 7)\n LANGUAGE sql\nAS $procedure$SELECT x$procedure$\n","INOUT x integer DEFAULT 7","INOUT x integer",nil})
	proceduresBoth(t,p,b,c,"CALL rd_inout()",[]string{"x"},[]string{"INT4"},[][]any{{int64(7)}})
	routineDeparseFour(t,p,b,c,"rd_frozen(integer)",[4]any{"CREATE OR REPLACE FUNCTION public.rd_frozen(a integer DEFAULT 1)\n RETURNS integer\n LANGUAGE sql\nRETURN (rd_leaf_renamed() + a)\n","a integer DEFAULT 1","a integer","integer"})
	resultsBoth(t,p,b,c,"SELECT rd_frozen() AS value",[]string{"value"},[]string{"INT4"},[][]any{{int64(5)}})
	for _,tc := range []struct{identity,args,identityArgs,result string}{
		{"rd_mix(integer,bigint,integer)","OUT first integer, x integer, INOUT total bigint, OUT note text, bonus integer DEFAULT 2","OUT first integer, x integer, INOUT total bigint, OUT note text, bonus integer","record"},
		{"rd_table(integer)","x integer","x integer","TABLE(a integer, b text)"},
		{"rd_set(integer)","x integer","x integer","SETOF integer"},
		{"rd_var(integer[])","VARIADIC xs integer[]","VARIADIC xs integer[]","integer[]"},
		{"rd_domain_result(rd_domain[])","x rd_domain[]","x rd_domain[]","rd_domain[]"},
		{"rd_pair_result(rd_pair)","x rd_pair","x rd_pair","rd_pair"},
		{"rd_poly(anyelement)","x anyelement","x anyelement","anyelement"},
		{"rd_void()","","","void"},
		{"rd_pl(integer)","a integer DEFAULT 7","a integer","integer"},
	} {
		for i,want := range []string{tc.args,tc.identityArgs,tc.result} {
			routineDeparseText(t,p,b,c,"SELECT "+routineDeparseNames[i+1]+"('"+tc.identity+"'::regprocedure) AS definition",want)
		}
	}
	routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('rd_pl(integer)'::regprocedure) AS definition","CREATE OR REPLACE FUNCTION public.rd_pl(a integer DEFAULT 7)\n RETURNS integer\n LANGUAGE plpgsql\nAS $function$BEGIN RETURN a; END$function$\n")
	routineDeparseFour(t,p,b,c,"rd_return()",[4]any{routineDeparseReturn,"","","integer"})
	routineDeparseFour(t,p,b,c,"rd_atomic()",[4]any{routineDeparseAtomic,"","","integer"})
	resultsBoth(t,p,b,c,"SELECT rd_return() AS r,rd_atomic() AS a,rd_pl() AS pl",[]string{"r","a","pl"},[]string{"INT4","INT4","INT4"},[][]any{{int64(7),int64(8),int64(7)}})
	resultsBothMetadata(t,p,b,c,"SELECT * FROM rd_mix(3,10)",[]string{"first","total","note"},[]string{"INT4","INT8","TEXT"},[][]any{{int64(3),int64(12),"ok"}},[]resultsNativeColumn{{},{},resultsTextMetadata})
	resultsBothMetadata(t,p,b,c,"SELECT * FROM rd_table(5)",[]string{"a","b"},[]string{"INT4","TEXT"},[][]any{{int64(5),"row"}},[]resultsNativeColumn{{},resultsTextMetadata})
	step := fixture.Select("SELECT * FROM rd_set(5)",[]string{"rd_set"},[]string{"INT4"},[][]any{{int64(5)},{int64(5)}})
	step.Bag = true
	fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(step))
}

func TestReferenceRoutineDeparseModesAndResults(t *testing.T) {
	fixture.DeparseRelationsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		routineDeparseModesSeed(t,p,b,c)
		routineDeparseModesCheck(t,p,b,c)
		for _,tc := range []struct{identity,drop string}{
			{"rd_p(integer)","DROP PROCEDURE rd_p(integer)"},
			{"rd_return()","DROP FUNCTION rd_return()"},
			{"rd_atomic()","DROP FUNCTION rd_atomic()"},
		} {
			definition := fixture.DeparseRelationsRead(t,p,b,c,"SELECT pg_get_functiondef('"+tc.identity+"'::regprocedure) AS definition")
			resultsExec(t,p,b,c,tc.drop,definition)
		}
		routineDeparseModesCheck(t,p,b,c)
		resultsExec(t,p,b,c,"CALL rd_p()","CALL rd_p(9)")
	})
}
