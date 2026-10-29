package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

var routineDeparseNames = []string{"pg_get_functiondef", "pg_get_function_arguments", "pg_get_function_identity_arguments", "pg_get_function_result"}

const routineDeparseDefault = "CREATE OR REPLACE FUNCTION public.rd_f(a integer DEFAULT 7)\n RETURNS integer\n LANGUAGE sql\nAS $function$SELECT $1$function$\n"
const routineDeparseAbs = "CREATE OR REPLACE FUNCTION pg_catalog.abs(integer)\n RETURNS integer\n LANGUAGE internal\n IMMUTABLE PARALLEL SAFE STRICT\nAS $function$int4abs$function$\n"

func routineDeparseText(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, want any, args ...any) {
	t.Helper()
	step := fixture.DeparseRelationsTextStep(t,b,q,want,args...)
	fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(step))
}

func routineDeparseFour(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, identity string, want [4]any) {
	t.Helper()
	for i, name := range routineDeparseNames {
		routineDeparseText(t,p,b,c,"SELECT "+name+"('"+identity+"'::regprocedure) AS definition",want[i])
	}
}

func routineDeparseSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	resultsExec(t,p,b,c,"CREATE FUNCTION public.rd_f(a integer DEFAULT 7) RETURNS integer LANGUAGE SQL AS 'SELECT $1'")
}

func TestReferenceRoutineDeparseSignatures(t *testing.T) {
	fixture.DeparseRelationsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for i,name := range routineDeparseNames {
			want := []any{routineDeparseAbs,"integer","integer","integer"}[i]
			for _,arg := range []string{"1397", "1397::oid", "'1397'", "'abs(integer)'::regprocedure"} {
				routineDeparseText(t,p,b,c,"SELECT "+name+"("+arg+") AS definition",want)
			}
			routineDeparseText(t,p,b,c,"SELECT pg_catalog."+name+"($1::oid) AS definition",want,int64(1397))
			q := "SELECT "+name+"(1397::oid)"
			for _,prepared := range []bool{false,true} {
				s := fixture.Select(q,[]string{name},[]string{"TEXT"},[][]any{{want}})
				if prepared { s = fixture.Prepared(s) }
				fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
			}
		}
		// Four actual catalog-owned declarations, not ledger-only aliases.
		q := "SELECT oid::bigint AS oid,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,proargtypes::text AS args,prorettype::bigint AS result FROM pg_proc WHERE oid IN (2098,2162,2165,2232) ORDER BY oid"
		want := [][]any{{int64(2098),true,"s","s","26",int64(25)},{int64(2162),true,"s","s","26",int64(25)},{int64(2165),true,"s","s","26",int64(25)},{int64(2232),true,"s","s","26",int64(25)}}
		resultsBothMetadata(t,p,b,c,q,[]string{"oid","proisstrict","volatility","parallel","args","result"},[]string{"INT8","BOOL","TEXT","TEXT","TEXT","INT8"},want,[]resultsNativeColumn{{},resultsNonNullMetadata,resultsTextMetadata,resultsTextMetadata,resultsTextMetadata,{}})
	})
}

func TestReferenceRoutineDeparseDeclarations(t *testing.T) {
	for _, pass := range []string{"first", "repeat"} {
		t.Run(pass, routineDeparseDeclarationsScenario)
	}
}

func routineDeparseDeclarationsScenario(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "A.B")
	fixture.DeparseRelationsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		routineDeparseSeed(t,p,b,c)
		want := [4]any{routineDeparseDefault,"a integer DEFAULT 7","a integer","integer"}
		routineDeparseFour(t,p,b,c,"rd_f(integer)",want)
		definition := fixture.DeparseRelationsRead(t,p,b,c,"SELECT pg_get_functiondef('rd_f(integer)'::regprocedure) AS definition")
		resultsExec(t,p,b,c,"DROP FUNCTION rd_f(integer)",definition)
		routineDeparseFour(t,p,b,c,"rd_f(integer)",want)
		resultsBoth(t,p,b,c,"SELECT rd_f() AS omitted,rd_f(9) AS supplied",[]string{"omitted","supplied"},[]string{"INT4","INT4"},[][]any{{int64(7),int64(9)}})
		resultsExec(t,p,b,c,"CREATE FUNCTION rd_zero() RETURNS integer LANGUAGE SQL AS 'SELECT 4'", "CREATE FUNCTION rd_collision() RETURNS text LANGUAGE SQL AS $$SELECT '$function'::text$$")
		routineDeparseText(t,p,b,c,"SELECT pg_get_function_arguments('rd_zero()'::regprocedure) AS definition","")
		routineDeparseText(t,p,b,c,"SELECT pg_get_function_identity_arguments('rd_zero()'::regprocedure) AS definition","")
		routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('rd_collision()'::regprocedure) AS definition","CREATE OR REPLACE FUNCTION public.rd_collision()\n RETURNS text\n LANGUAGE sql\nAS $functionx$SELECT '$function'::text$functionx$\n")
		resultsExec(t,p,b,c,"CREATE FUNCTION rd_defaults(a integer DEFAULT 1+2,b integer DEFAULT NULL,c text DEFAULT E'it''s\\\\here') RETURNS integer LANGUAGE SQL AS 'SELECT a'")
		routineDeparseFour(t,p,b,c,"rd_defaults(integer,integer,text)",[4]any{
			"CREATE OR REPLACE FUNCTION public.rd_defaults(a integer DEFAULT (1 + 2), b integer DEFAULT NULL::integer, c text DEFAULT 'it''s\\here'::text)\n RETURNS integer\n LANGUAGE sql\nAS $function$SELECT a$function$\n",
			"a integer DEFAULT (1 + 2), b integer DEFAULT NULL::integer, c text DEFAULT 'it''s\\here'::text","a integer, b integer, c text","integer",
		})
		resultsBoth(t,p,b,c,"SELECT rd_defaults() AS value",[]string{"value"},[]string{"INT4"},[][]any{{int64(3)}})
		resultsExec(t,p,b,c,"CREATE FUNCTION rd_typed(a varchar(12)) RETURNS varchar(3) LANGUAGE SQL SET search_path TO public,pg_catalog SET application_name TO E'a\\\\b' AS 'SELECT a'")
		config := "CREATE OR REPLACE FUNCTION public.rd_typed(a character varying)\n RETURNS character varying\n LANGUAGE sql\n SET search_path TO 'public', 'pg_catalog'\n SET application_name TO 'a\\b'\nAS $function$SELECT a$function$\n"
		routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('rd_typed(varchar)'::regprocedure) AS definition",config)
		resultsExec(t,p,b,c,"SET standard_conforming_strings=off")
		routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('rd_typed(varchar)'::regprocedure) AS definition","CREATE OR REPLACE FUNCTION public.rd_typed(a character varying)\n RETURNS character varying\n LANGUAGE sql\n SET search_path TO 'public', 'pg_catalog'\n SET application_name TO 'a\\\\b'\nAS $function$SELECT a$function$\n")
		resultsExec(t,p,b,c,"RESET standard_conforming_strings")
		resultsExec(t,p,b,c,"SET quote_all_identifiers=on")
		quoted := "CREATE OR REPLACE FUNCTION \"public\".\"rd_typed\"(\"a\" character varying)\n RETURNS character varying\n LANGUAGE \"sql\"\n SET \"search_path\" TO 'public', 'pg_catalog'\n SET \"application_name\" TO 'a\\b'\nAS $function$SELECT a$function$\n"
		routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('rd_typed(varchar)'::regprocedure) AS definition",quoted)
		resultsExec(t,p,b,c,"RESET quote_all_identifiers")
		resultsExec(t,p,b,c,"CREATE FUNCTION rd_space() RETURNS integer LANGUAGE SQL AS E' SELECT 4\\n'")
		routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('rd_space()'::regprocedure) AS definition","CREATE OR REPLACE FUNCTION public.rd_space()\n RETURNS integer\n LANGUAGE sql\nAS $function$ SELECT 4\n$function$\n")
		resultsExec(t,p,b,c,"CREATE FUNCTION rd_attr(a integer) RETURNS SETOF integer LANGUAGE SQL STABLE PARALLEL RESTRICTED STRICT SECURITY DEFINER COST 3.25 ROWS 8 AS 'SELECT a UNION ALL SELECT a'")
		routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('rd_attr(integer)'::regprocedure) AS definition","CREATE OR REPLACE FUNCTION public.rd_attr(a integer)\n RETURNS SETOF integer\n LANGUAGE sql\n STABLE PARALLEL RESTRICTED STRICT SECURITY DEFINER COST 3.25 ROWS 8\nAS $function$SELECT a UNION ALL SELECT a$function$\n")
		resultsExec(t,p,b,c,`CREATE SCHEMA "A.B"`, `CREATE FUNCTION "A.B"."Odd""Name"("select" integer DEFAULT 3) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`)
		routineDeparseFour(t,p,b,c,`"A.B"."Odd""Name"(integer)`,[4]any{"CREATE OR REPLACE FUNCTION \"A.B\".\"Odd\"\"Name\"(\"select\" integer DEFAULT 3)\n RETURNS integer\n LANGUAGE sql\nAS $function$SELECT $1$function$\n","\"select\" integer DEFAULT 3","\"select\" integer","integer"})
	})
}

func TestReferenceRoutineDeparseNullAndMissing(t *testing.T) {
	fixture.DeparseRelationsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM pg_proc WHERE oid IN (0,4294967295::oid)",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
		resultsExec(t,p,b,c,"CREATE TABLE rd_missing(i integer)")
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM pg_proc WHERE oid='rd_missing'::regclass::oid",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
		for _,name := range routineDeparseNames {
			for _,arg := range []string{"NULL","NULL::oid","0::oid","4294967295::oid","'rd_missing'::regclass::oid"} {
				routineDeparseText(t,p,b,c,"SELECT "+name+"("+arg+") AS definition",nil)
			}
		}
	})
}

func TestReferenceRoutineDeparseBuiltins(t *testing.T) {
	fixture.DeparseRelationsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		routineDeparseFour(t,p,b,c,"abs(integer)",[4]any{routineDeparseAbs,"integer","integer","integer"})
		for _,tc := range []struct{id int; name,args,result string}{
			{2803,"count","","bigint"},{3974,"percentile_cont","double precision ORDER BY double precision","double precision"},
		} {
			for _,name := range []string{"pg_get_function_arguments","pg_get_function_identity_arguments"} { routineDeparseText(t,p,b,c,fmt.Sprintf("SELECT %s(%d::oid) AS definition",name,tc.id),tc.args) }
			routineDeparseText(t,p,b,c,fmt.Sprintf("SELECT pg_get_function_result(%d::oid) AS definition",tc.id),tc.result)
			w := fixture.ControlsReferenceDiagnostic("42809",`"`+tc.name+`" is an aggregate function`)
			for _,prepared := range []bool{false,true} { routineDeparseFailure(t,p,b,c,fmt.Sprintf("SELECT pg_get_functiondef(%d::oid)",tc.id),w,prepared,true,nil) }
		}
		routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef(1708::oid) AS definition","CREATE OR REPLACE FUNCTION pg_catalog.round(numeric)\n RETURNS numeric\n LANGUAGE sql\n IMMUTABLE PARALLEL SAFE STRICT COST 1\nRETURN round($1, 0)\n")
		// Identity includes output formals, unlike regprocedure's input signature.
		routineDeparseText(t,p,b,c,"SELECT pg_get_function_identity_arguments('jsonb_each(jsonb)'::regprocedure) AS definition","from_json jsonb, OUT key text, OUT value jsonb")
		routineDeparseText(t,p,b,c,"SELECT pg_get_function_result('jsonb_each(jsonb)'::regprocedure) AS definition","SETOF record")
		routineDeparseText(t,p,b,c,"SELECT pg_get_function_arguments('concat(\"any\")'::regprocedure) AS definition","VARIADIC \"any\"")
		routineDeparseText(t,p,b,c,"SELECT pg_get_function_arguments(3986::oid) AS definition","VARIADIC \"any\" ORDER BY VARIADIC \"any\"")
		routineDeparseText(t,p,b,c,"SELECT pg_get_function_identity_arguments(3986::oid) AS definition","VARIADIC \"any\" ORDER BY VARIADIC \"any\"")
		routineDeparseFour(t,p,b,c,"row_number()",[4]any{"CREATE OR REPLACE FUNCTION pg_catalog.row_number()\n RETURNS bigint\n LANGUAGE internal\n WINDOW IMMUTABLE PARALLEL SAFE SUPPORT window_row_number_support\nAS $function$window_row_number$function$\n","","","bigint"})
		routineDeparseFour(t,p,b,c,"jsonb_set(jsonb,text[],jsonb,boolean)",[4]any{
			"CREATE OR REPLACE FUNCTION pg_catalog.jsonb_set(jsonb_in jsonb, path text[], replacement jsonb, create_if_missing boolean DEFAULT true)\n RETURNS jsonb\n LANGUAGE internal\n IMMUTABLE PARALLEL SAFE STRICT\nAS $function$jsonb_set$function$\n",
			"jsonb_in jsonb, path text[], replacement jsonb, create_if_missing boolean DEFAULT true",
			"jsonb_in jsonb, path text[], replacement jsonb, create_if_missing boolean","jsonb",
		})
		// Every represented nonaggregate descriptor must be presentable, including
		// metadata-only providers; ledger-only aliases cannot enlarge this set.
		facts := pgProcInventoryFacts(t)
		var expected int64
		for _,fact := range facts { if fact[8] != "a" { expected++ } }
		// WHERE conjuncts have no SQL evaluation-order guarantee. CASE provides
		// the required conditional demand: aggregates deliberately reject def().
		q := "SELECT count(*) FROM pg_proc WHERE oid IN ("+pgProcInventoryIDs(facts)+") AND CASE WHEN prokind <> 'a' THEN pg_get_functiondef(oid) IS NOT NULL AND pg_get_function_arguments(oid) IS NOT NULL AND pg_get_function_identity_arguments(oid) IS NOT NULL AND pg_get_function_result(oid) IS NOT NULL ELSE false END"
		step := fixture.Select(q,[]string{"count"},[]string{"INT8"},[][]any{{expected}})
		fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(step))
	})
}
