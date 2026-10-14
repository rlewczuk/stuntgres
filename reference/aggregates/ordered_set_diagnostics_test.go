package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOrderedSetAggregateDiagnostics(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		const undefined="No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{q,code,message,token,hint,detail string} {
			{"SELECT percentile_cont(0.5,1)","42809","WITHIN GROUP is required for ordered-set aggregate percentile_cont","percentile_cont","",""},
			{"SELECT percentile_cont(0.5)","42883","function percentile_cont(numeric) does not exist","percentile_cont",undefined,""},
			{"SELECT mode(1)","42809","WITHIN GROUP is required for ordered-set aggregate mode","mode","",""},
			{"SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY 1) OVER ()","0A000","OVER is not supported for ordered-set aggregate percentile_cont","percentile_cont","",""},
			{"SELECT sum() WITHIN GROUP(ORDER BY 1)","42809","sum is not an ordered-set aggregate, so it cannot have WITHIN GROUP","sum","",""},
			{"SELECT abs() WITHIN GROUP(ORDER BY 1)","42809","WITHIN GROUP specified, but abs is not an aggregate function","abs","",""},
			{"SELECT percentile_cont() WITHIN GROUP(ORDER BY 0.5,1)","42883","function percentile_cont(numeric, integer) does not exist","percentile_cont","There is an ordered-set aggregate percentile_cont, but it requires 1 direct argument, not 0.",""},
			{"SELECT percentile_cont(p) WITHIN GROUP(ORDER BY x) FROM os_input","42803","column \"os_input.p\" must appear in the GROUP BY clause or be used in an aggregate function","p)","","Direct arguments of an ordered-set aggregate must use only grouped columns."},
			{"SELECT percentile_cont(sum(x)) WITHIN GROUP(ORDER BY x) FROM os_input","42803","aggregate function calls cannot be nested","sum(x)","",""},
			{"SELECT mode() WITHIN GROUP(ORDER BY NULL::json)","42883","could not identify an ordering operator for type json","NULL","Use an explicit ordering operator or modify the query.",""},
			{"SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY TRUE)","42883","function percentile_cont(numeric, boolean) does not exist","percentile_cont",undefined,""},
			{"SELECT absent_schema.percentile_cont(.5) WITHIN GROUP(ORDER BY 1)","3F000","schema \"absent_schema\" does not exist","absent_schema","",""},
			{"/*é😀*/ SELECT percentile_cont(.5)","42883","function percentile_cont(numeric) does not exist","percentile_cont",undefined,""},
		} {
			t.Run(tc.q,func(t *testing.T) {
				s:=orderedSetDiagnostic(tc.q,tc.code,tc.message,tc.token,tc.hint)
				s.Diagnostic.Detail=tc.detail
				orderedSetBoth(t,p,c,b,s)
			})
		}
	})
}

func TestReferenceOrderedSetAggregatePrecedence(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		for _, tc := range []struct{q,code,message,token string} {
			{"SELECT percentile_cont(0.5 ORDER BY 1) WITHIN GROUP(ORDER BY 1)","42601","cannot use multiple ORDER BY clauses with WITHIN GROUP","WITHIN"},
			{"SELECT percentile_cont(DISTINCT 0.5 ORDER BY 1) WITHIN GROUP(ORDER BY 1)","42601","cannot use multiple ORDER BY clauses with WITHIN GROUP","WITHIN"},
			{"SELECT percentile_cont(DISTINCT 0.5) WITHIN GROUP(ORDER BY 1)","42601","cannot use DISTINCT with WITHIN GROUP","WITHIN"},
			{"SELECT percentile_cont(VARIADIC ARRAY[.5]) WITHIN GROUP(ORDER BY 1)","42601","cannot use VARIADIC with WITHIN GROUP","WITHIN"},
			{"SELECT percentile_cont(missing) WITHIN GROUP(ORDER BY x) FROM os_input","42703","column \"missing\" does not exist","missing"},
			{"SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY x) FILTER(WHERE 2) FROM os_input","42804","argument of FILTER must be type boolean, not type integer","2)"},
			{"SELECT x FROM os_input WHERE mode() WITHIN GROUP(ORDER BY x)=1","42803","aggregate functions are not allowed in WHERE","mode"},
			{"SELECT x FROM os_input GROUP BY mode() WITHIN GROUP(ORDER BY x)","42803","aggregate functions are not allowed in GROUP BY","mode"},
			{"SELECT percentile_cont(.5) WITHIN (ORDER BY 1)","42601","syntax error at or near \"(\"","(ORDER"},
			{"SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY)","42601","syntax error at or near \")\"", "BY)"},
		} {
			t.Run(tc.q,func(t *testing.T){
				s:=orderedSetDiagnostic(tc.q,tc.code,tc.message,tc.token,"")
				if tc.token=="BY)" {s.Diagnostic.Position=fixture.Position(len(tc.q))}
				orderedSetBoth(t,p,c,b,s)
			})
		}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE os_clause(id INT,v FLOAT8)",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO os_clause VALUES(99,99)",1))
		unchanged:=fixture.Select("SELECT id,v FROM os_clause ORDER BY id",[]string{"id","v"},[]string{"INT4","FLOAT8"},[][]any{{int64(99),float64(99)}})
		for _,family:=range []string{"percentile_cont(.5)","percentile_disc(.5)","mode()"} {
			token:="percentile_cont";if family=="percentile_disc(.5)" {token="percentile_disc"};if family=="mode()" {token="mode"}
			for _,tc:=range []struct{name,q,clause string}{
				{"values","VALUES ("+family+" WITHIN GROUP(ORDER BY 1))","VALUES"},
				{"insert_values","/*é😀*/ INSERT INTO os_clause VALUES(1,1),(2,"+family+" WITHIN GROUP(ORDER BY 1))","VALUES"},
				{"insert_returning","INSERT INTO os_clause VALUES(1,1) RETURNING "+family+" WITHIN GROUP(ORDER BY v)","RETURNING"},
				{"update_returning","UPDATE os_clause SET v=0 RETURNING "+family+" WITHIN GROUP(ORDER BY v)","RETURNING"},
			} {
				for _,prepared:=range []bool{false,true} {
					for _,query:=range []bool{true,false} {
						name:=family+"/"+tc.name+"/direct";if prepared {name=family+"/"+tc.name+"/prepared"}
						if query {name+="/query"} else {name+="/exec"}
						t.Run(name,func(t *testing.T){
							// Check state even when the diagnostic assertion fails:
							// neither a VALUES prefix nor RETURNING may publish writes.
							defer fixture.RunStep(t,p.Ctx,c,b.Recorder,unchanged)
							s:=orderedSetDiagnostic(tc.q,"42803","aggregate functions are not allowed in "+tc.clause,token,"")
							s.Prepared=prepared;s.SelectRows=query;s.ErrorStage="execute"
							if prepared {s.ErrorStage="prepare"}
							fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
						})
					}
				}
			}
		}
	})
}
