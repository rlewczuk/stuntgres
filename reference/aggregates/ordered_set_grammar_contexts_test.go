package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOrderedSetAggregateGrammarContexts(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend){
		orderedSetSetup(t,p,c,b)
		// State 852 must shift WITHIN onto this selected ordinary function,
		// not reduce the function followed by an output alias named WITHIN.
		for _,q:=range []string{
			"SELECT abs() WITHIN GROUP(ORDER BY 1)",
			"SELECT abs(abs() WITHIN GROUP(ORDER BY x)) AS v FROM os_input",
		} {
			orderedSetBoth(t,p,c,b,orderedSetDiagnostic(q,"42809","WITHIN GROUP specified, but abs is not an aggregate function","abs()",""))
		}
		// State 926 attaches the aggregate clause before FILTER and before
		// returning the completed call to its enclosing scalar expression.
		for _,tc:=range []struct{q string;want float64}{
			{"SELECT abs(percentile_cont(.25) WITHIN GROUP(ORDER BY x) FILTER(WHERE x IS NOT NULL)) AS v FROM os_input",17.5},
			{"SELECT (percentile_cont(.25) WITHIN GROUP(ORDER BY x))+1 AS v FROM os_input",18.5},
			{"SELECT pg_catalog.\"percentile_cont\"(.5) WITHIN GROUP(ORDER BY abs(x)) AS v FROM os_input",25},
		} {orderedSetBoth(t,p,c,b,orderedSetValue(tc.q,"FLOAT8",tc.want))}
		q:="SELECT abs(percentile_cont(.5) WITHIN GROUP(ORDER BY x) OVER ()) AS v FROM os_input"
		orderedSetBoth(t,p,c,b,orderedSetDiagnostic(q,"0A000","OVER is not supported for ordered-set aggregate percentile_cont","percentile_cont",""))
		// Public index expressions require parentheses around WITHIN calls.
		// The parser-only direct-index witness separately maps native state
		// 2278; that broader native grammar is not a PostgreSQL success claim.
		q="CREATE INDEX os_grammar_function_index ON os_input((pg_catalog.overlaps(TIME '00:00',TIME '01:00',TIME '02:00') WITHIN GROUP(ORDER BY TIME '03:00')))"
		s:=orderedSetDiagnostic(q,"42809","WITHIN GROUP specified, but pg_catalog.overlaps is not an aggregate function","pg_catalog","");s.SelectRows=false
		s.ErrorStage="execute"
		orderedSetBoth(t,p,c,b,s)
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT count(*) AS v FROM pg_catalog.pg_class WHERE relname='os_grammar_function_index'","INT8",int64(0)))
		q="CREATE INDEX os_grammar_aggregate_index ON os_input((percentile_cont(.5) WITHIN GROUP(ORDER BY x)))"
		s=orderedSetDiagnostic(q,"42803","aggregate functions are not allowed in index expressions","percentile_cont","");s.SelectRows=false
		s.ErrorStage="execute"
		orderedSetBoth(t,p,c,b,s)
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT count(*) AS v FROM pg_catalog.pg_class WHERE relname='os_grammar_aggregate_index'","INT8",int64(0)))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE within(mode INT,within INT)",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO within VALUES(1,2),(3,4)",2))
		orderedSetBoth(t,p,c,b,fixture.Select("SELECT mode,within FROM within ORDER BY mode",[]string{"mode","within"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(2)},{int64(3),int64(4)}}))
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT mode() WITHIN GROUP(ORDER BY mode) AS v FROM within","INT4",int64(1)))
		orderedSetBoth(t,p,c,b,fixture.Select("SELECT mode AS \"WITHIN\",within AS \"MODE\" FROM within ORDER BY mode",[]string{"WITHIN","MODE"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(2)},{int64(3),int64(4)}}))
		orderedSetBoth(t,p,c,b,fixture.Select("SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY x) AS within FROM os_input",[]string{"within"},[]string{"FLOAT8"},[][]any{{float64(25)}}))
	})
}
