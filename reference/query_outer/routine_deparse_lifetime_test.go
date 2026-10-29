package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineDeparseLifetime(t *testing.T) {
	for _, pass := range []string{"first", "repeat"} {
		t.Run(pass, routineDeparseLifetimeScenario)
	}
}

func routineDeparseLifetimeScenario(t *testing.T) {
	// LIFO cleanup closes the pair first, then removes schemas, then the role.
	fixture.RoleReferenceCleanup(t, []string{"rd_reader"})
	fixture.SchemaReferenceCleanup(t, "Moved", "shadow")
	fixture.DeparseRelationsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		routineDeparseSeed(t,p,b,c)
		var oid int64
		if err := c.QueryRowContext(p.Ctx,"SELECT 'rd_f(integer)'::regprocedure::oid::bigint").Scan(&oid);err != nil { t.Fatal(err) }
		q := "SELECT pg_get_functiondef($1::oid) AS definition"
		held,err := c.PrepareContext(p.Ctx,q)
		if err != nil { t.Fatal(err) };defer held.Close()
		old := fixture.DeparseRelationsRead(t,p,b,c,q,oid)
		resultsExec(t,p,b,c,"CREATE OR REPLACE FUNCTION rd_f(a integer DEFAULT 7) RETURNS integer LANGUAGE SQL AS 'SELECT $1+1'", "CREATE SCHEMA \"Moved\"", "ALTER FUNCTION rd_f(integer) RENAME TO \"Renamed\"", "ALTER FUNCTION \"Renamed\"(integer) SET SCHEMA \"Moved\"", "SET quote_all_identifiers=on")
		want := "CREATE OR REPLACE FUNCTION \"Moved\".\"Renamed\"(\"a\" integer DEFAULT 7)\n RETURNS integer\n LANGUAGE \"sql\"\nAS $function$SELECT $1+1$function$\n"
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:q},b.Recorder,fixture.DeparseRelationsTextStep(t,b,q,want,oid))
		if old != routineDeparseDefault { t.Fatal("retained output mutated",old) }
		resultsExec(t,p,b,c,"RESET quote_all_identifiers",
			"CREATE SEQUENCE rd_counter START 40",
			"CREATE FUNCTION rd_side(a bigint DEFAULT nextval('rd_counter'::regclass)) RETURNS bigint LANGUAGE SQL AS 'SELECT a'",
			"CREATE FUNCTION rd_leaf() RETURNS integer LANGUAGE SQL AS 'SELECT 1'",
			"CREATE FUNCTION rd_body() RETURNS integer RETURN rd_leaf()",
			"CREATE FUNCTION rd_quoted() RETURNS integer LANGUAGE SQL AS 'SELECT rd_leaf()'",
			"ALTER FUNCTION rd_leaf() RENAME TO rd_leaf_renamed",
			"CREATE FUNCTION rd_leaf() RETURNS integer LANGUAGE SQL AS 'SELECT 9'")
		// Exercise ordinary frozen/quoted calls before implementation replacement.
		// Metadata display after replacement is independent of executable admission.
		resultsBoth(t,p,b,c,"SELECT rd_body() AS frozen,rd_quoted() AS quoted",[]string{"frozen","quoted"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(9)}})
		resultsExec(t,p,b,c,"CREATE OR REPLACE FUNCTION rd_leaf_renamed() RETURNS integer LANGUAGE SQL AS 'SELECT 2'",
			"REVOKE EXECUTE ON FUNCTION rd_side(bigint) FROM PUBLIC",
			"CREATE ROLE rd_reader", "GRANT USAGE ON SCHEMA public TO rd_reader", "SET ROLE rd_reader")
		routineDeparseText(t,p,b,c,"SELECT pg_get_function_arguments('rd_side(bigint)'::regprocedure) AS definition","a bigint DEFAULT nextval('rd_counter'::regclass)")
		routineDeparseText(t,p,b,c,"SELECT pg_get_function_identity_arguments('rd_side(bigint)'::regprocedure) AS definition","a bigint")
		routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('rd_body()'::regprocedure) AS definition","CREATE OR REPLACE FUNCTION public.rd_body()\n RETURNS integer\n LANGUAGE sql\nRETURN rd_leaf_renamed()\n")
		routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('rd_quoted()'::regprocedure) AS definition","CREATE OR REPLACE FUNCTION public.rd_quoted()\n RETURNS integer\n LANGUAGE sql\nAS $function$SELECT rd_leaf()$function$\n")
		resultsExec(t,p,b,c,"RESET ROLE")
		resultsBothMetadata(t,p,b,c,"SELECT last_value,is_called FROM rd_counter",[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(40),false}},[]resultsNativeColumn{resultsNonNullMetadata,resultsNonNullMetadata})
		// Inspecting retained metadata above did not execute the counter default.
		// The replaced leaf itself remains ordinarily callable.
		resultsBoth(t,p,b,c,"SELECT rd_leaf_renamed() AS replacement,rd_quoted() AS quoted",[]string{"replacement","quoted"},[]string{"INT4","INT4"},[][]any{{int64(2),int64(9)}})
		resultsExec(t,p,b,c,"CREATE DOMAIN rd_domain AS integer", "CREATE FUNCTION rd_domain_f(a rd_domain) RETURNS rd_domain LANGUAGE SQL AS 'SELECT a'", "CREATE SCHEMA shadow", "CREATE DOMAIN shadow.rd_domain AS text", "SET search_path TO shadow,public")
		routineDeparseText(t,p,b,c,"SELECT pg_get_function_arguments('public.rd_domain_f(public.rd_domain)'::regprocedure) AS definition","a public.rd_domain")
		routineDeparseText(t,p,b,c,"SELECT pg_get_function_result('public.rd_domain_f(public.rd_domain)'::regprocedure) AS definition","public.rd_domain")
		resultsExec(t,p,b,c,"RESET search_path")
		// Local overload visibility is ordinary callable competition, not a
		// special schema bypass in the inquiry binder.
		resultsExec(t,p,b,c,"CREATE FUNCTION public.pg_get_function_result(text) RETURNS text LANGUAGE SQL AS 'SELECT $1'", "SET search_path TO public,pg_catalog")
		resultsBothMetadata(t,p,b,c,"SELECT pg_get_function_result('local'::text) AS value",[]string{"value"},[]string{"TEXT"},[][]any{{"local"}},[]resultsNativeColumn{resultsTextMetadata})
		routineDeparseText(t,p,b,c,"SELECT pg_catalog.pg_get_function_result(1397::oid) AS definition","integer")
		resultsExec(t,p,b,c,"RESET search_path")
		text := fixture.DeparseRelationsRead(t,p,b,c,"SELECT pg_get_functiondef('rd_side(bigint)'::regprocedure) AS definition")
		if !strings.Contains(text,"DEFAULT nextval('rd_counter'::regclass)") { t.Fatal(text) }
	})
}

func TestReferenceRoutineDeparseTemporarySources(t *testing.T) {
	for _, pass := range []string{"first", "repeat"} {
		t.Run(pass, routineDeparseTemporarySourcesScenario)
	}
}

func routineDeparseTemporarySourcesScenario(t *testing.T) {
	// Public objects and the borrowed connection's TEMP objects use the normal
	// pair/session teardown; no additional schema or cluster role is created.
	fixture.DeparseRelationsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		resultsExec(t,p,b,c,
			"CREATE TEMP TABLE rd_temp_source(id integer)",
			"CREATE FUNCTION public.rd_temp_body() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT id FROM rd_temp_source; END",
			"CREATE TABLE public.rd_temp_source(id integer)",
			"SET search_path TO public,pg_temp")
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO pg_temp.rd_temp_source VALUES(7)",1))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO public.rd_temp_source VALUES(9)",1))
		var oid int64
		if err := c.QueryRowContext(p.Ctx,"SELECT 'public.rd_temp_body()'::regprocedure::oid::bigint").Scan(&oid);err != nil { t.Fatal(err) }
		q := "SELECT pg_get_functiondef($1::oid) AS definition"
		held,err := c.PrepareContext(p.Ctx,q)
		if err != nil { t.Fatal(err) };defer held.Close()
		// Independently observed on the pinned PG18.6 oracle: the permanent
		// masking relation forces qualification, but own-session TEMP is pg_temp,
		// not a physical namespace or the stored routine owner's namespace.
		for _,tc := range []struct{setting,want string}{
			{"off","CREATE OR REPLACE FUNCTION public.rd_temp_body()\n RETURNS integer\n LANGUAGE sql\nBEGIN ATOMIC\n SELECT rd_temp_source.id\n    FROM pg_temp.rd_temp_source;\nEND\n"},
			{"on","CREATE OR REPLACE FUNCTION \"public\".\"rd_temp_body\"()\n RETURNS integer\n LANGUAGE \"sql\"\nBEGIN ATOMIC\n SELECT \"rd_temp_source\".\"id\"\n    FROM \"pg_temp\".\"rd_temp_source\";\nEND\n"},
		} {
			resultsExec(t,p,b,c,"SET quote_all_identifiers="+tc.setting)
			routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('public.rd_temp_body()'::regprocedure) AS definition",tc.want)
			fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:q},b.Recorder,fixture.DeparseRelationsTextStep(t,b,q,tc.want,oid))
		}
		resultsExec(t,p,b,c,"RESET quote_all_identifiers")
		resultsBoth(t,p,b,c,"SELECT public.rd_temp_body() AS value",[]string{"value"},[]string{"INT4"},[][]any{{int64(7)}})
		resultsExec(t,p,b,c,"RESET search_path")
	})
}
