package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func aggregateOrderedTypePhases(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	for _,name:=range []string{"ua_rank","ua_dense","ua_percent","ua_cume"}{
		for _,tc:=range []struct{direct,order,code,message,token,hint string}{
			{"NULL::json,1","NULL::json,'x'::text","42804","WITHIN GROUP types text and integer cannot be matched","1)",""},
			{"'a' COLLATE \"C\",1","'a' COLLATE \"POSIX\",'x'::text","42804","WITHIN GROUP types text and integer cannot be matched","1)",""},
			{"'a' COLLATE \"C\",'bad'","'a' COLLATE \"POSIX\",1","22P02","invalid input syntax for type integer: \"bad\"","'bad'",""},
			{"'a' COLLATE \"C\",NULL::json","'a' COLLATE \"POSIX\",NULL::json","42883","could not identify an ordering operator for type json","","Use an explicit ordering operator or modify the query."},
		}{
			q:="/*é😀*/ SELECT "+name+"("+tc.direct+") WITHIN GROUP(ORDER BY "+tc.order+")"
			s:=orderedSetDiagnostic(q,tc.code,tc.message,tc.token,tc.hint)
			if tc.code=="42883" {s.Diagnostic.Position=fixture.Position(utf8.RuneCountInString(q[:strings.LastIndex(q,"NULL::json")])+1)}
			orderedSetBoth(t,p,c,b,s)
		}
	}
	q:=`SELECT ua_rank('a' COLLATE "C") WITHIN GROUP(ORDER BY 'a' COLLATE "POSIX")`
	orderedSetBoth(t,p,c,b,orderedSetDiagnostic(q,"42P21","collation mismatch between explicit collations \"C\" and \"POSIX\"",`COLLATE "POSIX"`,""))
	// SQL cannot manufacture the opaque INTERNAL state: ordinary scalar
	// resolution fails before the aggregate-context support can execute.
	q="SELECT pg_catalog.ordered_set_transition_multi(NULL,1)"
	orderedSetBoth(t,p,c,b,orderedSetDiagnostic(q,"42883","function pg_catalog.ordered_set_transition_multi(unknown, integer) does not exist","pg_catalog.ordered_set_transition_multi","No function matches the given name and argument types. You might need to add explicit type casts."))
}

func aggregateOrderedTypeAuthorization(t *testing.T) {
	for _,slot:=range []string{"direct","row","state"}{t.Run(slot,func(t *testing.T){
		fixture.RoleReferenceCleanup(t,[]string{"ua_type_owner"})
		createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
			exec:=func(q string){createAggregateRun(t,p,b,c,fixture.Exec(q,0))}
			for _,q:=range []string{"CREATE ROLE ua_type_owner","GRANT USAGE,CREATE ON SCHEMA public TO ua_type_owner","CREATE DOMAIN ua_key AS integer"}{exec(q)}
			signature,state,step,final:="ua_key ORDER BY integer","integer","pg_catalog.int4pl","ua_type_final"
			switch slot {
			case "direct":exec("CREATE FUNCTION ua_type_final(integer,ua_key) RETURNS text LANGUAGE SQL AS $$SELECT ($1+$2)::text$$")
			case "row":signature="integer ORDER BY ua_key";step="ua_type_step";exec("CREATE FUNCTION ua_type_step(integer,ua_key) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$");exec("CREATE FUNCTION ua_type_final(integer,integer) RETURNS text LANGUAGE SQL AS $$SELECT ($1+$2)::text$$")
			case "state":signature="integer ORDER BY integer";state="ua_key";step="ua_type_step";exec("CREATE FUNCTION ua_type_step(ua_key,integer) RETURNS ua_key LANGUAGE SQL AS $$SELECT ($1+$2)::ua_key$$");exec("CREATE FUNCTION ua_type_final(ua_key,integer) RETURNS text LANGUAGE SQL AS $$SELECT ($1+$2)::text$$")
			}
			for _,q:=range []string{"REVOKE USAGE ON TYPE ua_key FROM PUBLIC","SET SESSION AUTHORIZATION ua_type_owner"}{exec(q)}
			defer c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
			ddl:="CREATE AGGREGATE ua_rejected("+signature+") (SFUNC="+step+",STYPE="+state+",INITCOND='0',FINALFUNC="+final+")"
			s:=fixture.ImplicitError(ddl,fixture.Diagnostic{Code:"42501",Message:"permission denied for type ua_key"});s.ErrorStage="execute";createAggregateBoth(t,p,b,c,s);aggregateOrderedNoResidue(t,p,b,c)
			for _,q:=range []string{"RESET SESSION AUTHORIZATION","GRANT USAGE ON TYPE ua_key TO ua_type_owner","SET SESSION AUTHORIZATION ua_type_owner",ddl}{exec(q)}
			createAggregateBoth(t,p,b,c,aggregateFinalText("SELECT ua_rejected(10) WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(2),(3)) s(x)","16"));exec("RESET SESSION AUTHORIZATION")
		})
	})}
}
