package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func aggregateOrderedNoResidue(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	for _,q:=range []string{
		"SELECT count(*) AS n FROM pg_proc WHERE proname='ua_rejected'",
		"SELECT count(*) AS n FROM(SELECT oid FROM pg_proc WHERE proname='ua_rejected') p JOIN pg_aggregate a ON a.aggfnoid=p.oid",
		"SELECT count(*) AS n FROM(SELECT oid FROM pg_proc WHERE proname='ua_rejected') p JOIN pg_depend d ON d.objid=p.oid WHERE d.classid='pg_proc'::regclass",
	}{createAggregateRun(t,p,b,c,fixture.Select(q,[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))}
}

func TestReferenceAggregateOrderedDiagnostics(t *testing.T) {
	for _,tc:=range []struct{name,signature,options,code,message,token string}{
		{"ordinary_hypothetical","integer","HYPOTHETICAL","42P13","only ordered-set aggregates can be hypothetical",""},
		{"missing_pair","ORDER BY integer","HYPOTHETICAL","42P13","a hypothetical-set aggregate must have direct arguments matching its aggregated arguments",""},
		{"pair_type","text ORDER BY integer","HYPOTHETICAL","42P13","a hypothetical-set aggregate must have direct arguments matching its aggregated arguments",""},
		{"declared_argument_cap","ORDER BY "+strings.TrimSuffix(strings.Repeat("integer,",100),","),"","54023","aggregates cannot have more than 99 arguments",""},
		{"variadic_array","ORDER BY VARIADIC integer[]","","0A000","a variadic ordered-set aggregate must use VARIADIC type ANY",""},
		{"variadic_scalar","ORDER BY VARIADIC integer","","42P13","VARIADIC parameter must be an array",""},
		{"variadic_not_last","ORDER BY VARIADIC integer[],integer","","42P13","VARIADIC parameter must be the last input parameter",""},
		{"direct_variadic_fixed_row","VARIADIC integer[] ORDER BY integer","","0A000","an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type","integer)"},
		{"direct_variadic_many_rows","VARIADIC integer[] ORDER BY integer,integer","","0A000","an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type","integer,integer"},
		{"direct_variadic_type","VARIADIC integer[] ORDER BY VARIADIC text[]","","0A000","an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type","VARIADIC text[]"},
		{"out","ORDER BY OUT x integer","","0A000","aggregates cannot have output arguments","OUT x"},
		{"inout","INOUT x integer ORDER BY integer","","0A000","aggregates cannot have output arguments","INOUT x"},
		{"strict_extra","integer ORDER BY integer","FINALFUNC=pg_catalog.int4_sum,FINALFUNC_EXTRA","42883","function pg_catalog.int4_sum(integer, integer, integer) does not exist",""},
		{"strict_final","ORDER BY integer","FINALFUNC=pg_catalog.int4pl,FINALFUNC_EXTRA","42P13","final function with extra arguments must not be declared STRICT",""},
		{"bad_boolean","ORDER BY integer","HYPOTHETICAL='bad'","42601","hypothetical requires a Boolean value",""},
		{"numeric_boolean","ORDER BY integer","HYPOTHETICAL=2","42601","hypothetical requires a Boolean value",""},
		{"missing_schema","ORDER BY integer","FINALFUNC=ua_missing.finish","3F000","schema \"ua_missing\" does not exist",""},
		{"missing_support","ORDER BY integer","FINALFUNC=ua_absent","42883","function ua_absent(integer) does not exist",""},
		{"missing_direct_final","text,boolean ORDER BY integer","FINALFUNC=ua_finish","42883","function ua_finish(integer[], text, boolean) does not exist",""},
		{"set_final","integer ORDER BY integer","FINALFUNC=pg_catalog.generate_series","42804","function pg_catalog.generate_series(integer, integer) returns a set",""},
		{"runtime_cast","ORDER BY integer","FINALFUNC=ua_wide","42804","function ua_wide(bigint) requires run-time type coercion",""},
	}{
		t.Run(tc.name,func(t *testing.T){createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
			createAggregateRun(t,p,b,c,fixture.Exec("CREATE FUNCTION ua_wide(bigint) RETURNS text LANGUAGE SQL AS $$SELECT $1::text$$",0))
			support,state,seed:="pg_catalog.int4pl","integer","0"
			if tc.name=="missing_direct_final" {aggregateOrderedSetup(t,p,b,c);support,state,seed="pg_catalog.array_append","integer[]","{}"}
			q:="CREATE AGGREGATE ua_rejected("+tc.signature+") (SFUNC="+support+",STYPE="+state+",INITCOND='"+seed+"'"
			if tc.options!="" {q+=","+tc.options};q+=")"
			for _,prepared:=range []bool{false,true}{
				s:=orderedSetDiagnostic(q,tc.code,tc.message,tc.token,"");s.Prepared=prepared;s.SelectRows=false;s.ErrorStage="execute"
				if prepared&&tc.token!="" {s.ErrorStage="prepare"}
				// Function-parameter validation runs at DDL execution, although
				// PostgreSQL exposes the parameter's source position.
				switch tc.name {
				case "variadic_scalar":s.Diagnostic.Position=fixture.Position(39)
				case "variadic_not_last":s.Diagnostic.Position=fixture.Position(58)
				}
				fixture.RunStep(t,p.Ctx,c,b.Recorder,s);aggregateOrderedNoResidue(t,p,b,c)
			}
			createAggregateRun(t,p,b,c,fixture.Exec("CREATE AGGREGATE ua_rejected(ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')",0))
			createAggregateBoth(t,p,b,c,createAggregateValue("SELECT ua_rejected() WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(2),(3)) s(x)",int64(6)))
		})})
	}
	t.Run("call_shapes",func(t *testing.T){createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		aggregateOrderedSetup(t,p,b,c);aggregateOrderedRanks(t,p,b,c)
		createAggregateRun(t,p,b,c,fixture.Exec("CREATE AGGREGATE ua_ordinary(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')",0))
		for _,tc:=range []struct{q,code,message,token,hint,detail string}{
			{"SELECT ua_sorted('p:',false,x) FROM ua_input","42809","WITHIN GROUP is required for ordered-set aggregate ua_sorted","ua_sorted","",""},
			{"SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) OVER() FROM ua_input","0A000","OVER is not supported for ordered-set aggregate ua_sorted","ua_sorted","",""},
			{"SELECT ua_ordinary() WITHIN GROUP(ORDER BY x) FROM ua_input","42809","ua_ordinary is not an ordered-set aggregate, so it cannot have WITHIN GROUP","ua_ordinary","",""},
			{"SELECT ua_sorted('p:') WITHIN GROUP(ORDER BY false,x) FROM ua_input","42883","function ua_sorted(unknown, boolean, integer) does not exist","ua_sorted","There is an ordered-set aggregate ua_sorted, but it requires 2 direct arguments, not 1.",""},
			{"SELECT ua_rank(1) WITHIN GROUP(ORDER BY x,g) FROM ua_input","42883","function ua_rank(integer, integer, integer) does not exist","ua_rank","To use the hypothetical-set aggregate ua_rank, the number of hypothetical direct arguments (here 1) must match the number of ordering columns (here 2).",""},
			{"SELECT ua_rank(1) WITHIN GROUP(ORDER BY 'a'::text)","42804","WITHIN GROUP types text and integer cannot be matched","1)","",""},
			{"SELECT ua_sorted(x::text,false) WITHIN GROUP(ORDER BY x) FROM ua_input t","42803","column \"t.x\" must appear in the GROUP BY clause or be used in an aggregate function","x::text","","Direct arguments of an ordered-set aggregate must use only grouped columns."},
			{"SELECT ua_sorted(DISTINCT 'p:',false) WITHIN GROUP(ORDER BY x) FROM ua_input","42601","cannot use DISTINCT with WITHIN GROUP","WITHIN","",""},
			{"SELECT ua_sorted('p:',false ORDER BY x) WITHIN GROUP(ORDER BY x) FROM ua_input","42601","cannot use multiple ORDER BY clauses with WITHIN GROUP","WITHIN","",""},
			{"SELECT ua_rank(VARIADIC ARRAY[1]) WITHIN GROUP(ORDER BY x) FROM ua_input","42601","cannot use VARIADIC with WITHIN GROUP","WITHIN","",""},
			{"SELECT x FROM ua_input WHERE ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) IS NULL","42803","aggregate functions are not allowed in WHERE","ua_sorted","",""},
			{"SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY x) FILTER(WHERE 1) FROM ua_input","42804","argument of FILTER must be type boolean, not type integer","1)","",""},
			{"SELECT ua_sorted('p:',false) WITHIN GROUP(ORDER BY sum(x)::integer) FROM ua_input","42803","aggregate function calls cannot be nested","sum","",""},
		}{
			s:=orderedSetDiagnostic(tc.q,tc.code,tc.message,tc.token,tc.hint);s.Diagnostic.Detail=tc.detail
			orderedSetBoth(t,p,c,b,s)
		}
		aggregateOrderedTypePhases(t,p,b,c)
		q:="SELECT ua_rank('x') WITHIN GROUP(ORDER BY x) FROM ua_input"
		orderedSetBoth(t,p,c,b,orderedSetDiagnostic(q,"22P02","invalid input syntax for type integer: \"x\"","'x'",""))
		q="SELECT ua_rank("+strings.TrimSuffix(strings.Repeat("1,",50),",")+") WITHIN GROUP(ORDER BY "+strings.TrimSuffix(strings.Repeat("x,",50),",")+") FROM ua_input"
		orderedSetBoth(t,p,c,b,orderedSetDiagnostic(q,"54023","aggregates cannot have more than 99 arguments","ua_rank",""))
	})})
	t.Run("variadic_declaration_support",func(t *testing.T){createAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,tc:=range []struct{signature,support,code,message string}{
			{"ORDER BY VARIADIC \"any\"","pg_catalog.ordered_set_transition_multi","42P13","a hypothetical-set aggregate must have direct arguments matching its aggregated arguments"},
			{"VARIADIC \"any\" ORDER BY VARIADIC \"any\"","pg_catalog.ordered_set_transition","42804","function pg_catalog.ordered_set_transition(internal, \"any\") must accept VARIADIC ANY to be used in this aggregate"},
		}{
			q:="CREATE AGGREGATE ua_rejected("+tc.signature+") (SFUNC="+tc.support+",STYPE=internal,FINALFUNC=pg_catalog.rank_final,FINALFUNC_EXTRA,HYPOTHETICAL)"
			s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:tc.code,Message:tc.message});s.ErrorStage="execute";createAggregateBoth(t,p,b,c,s);aggregateOrderedNoResidue(t,p,b,c)
		}
	})})
}
