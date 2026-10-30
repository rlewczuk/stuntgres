package reference_test

import (
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLAggDiagnostics(t *testing.T) {
	xmlAggScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE xa(k int,x xml)",0))
		const hint="No function matches the given name and argument types. You might need to add explicit type casts."
		for _,tc:=range []struct{q,code,msg,hint string;pos int}{
			{"SELECT xmlagg(1) FROM xa","42883","function xmlagg(integer) does not exist",hint,8},
			{"SELECT xmlagg() FROM xa","42883","function xmlagg() does not exist",hint,8},
			{"SELECT xmlagg(*) FROM xa","42883","function xmlagg() does not exist",hint,8},
			{"SELECT xmlagg(x,x) FROM xa","42883","function xmlagg(xml, xml) does not exist",hint,8},
			{"SELECT xmlagg(x ORDER BY x) FROM xa","42883","could not identify an ordering operator for type xml","Use an explicit ordering operator or modify the query.",26},
			{"SELECT xmlagg(DISTINCT x) FROM xa","42883","could not identify an equality operator for type xml","",24},
			{"SELECT xmlagg(x ORDER BY missing) FROM xa","42703",`column "missing" does not exist`,"",26},
			{"SELECT xmlagg(x ORDER BY missing) FROM xa WHERE false","42703",`column "missing" does not exist`,"",26},
			{"SELECT 'é' AS marker,xmlagg(1) FROM xa","42883","function xmlagg(integer) does not exist",hint,22},
			{"SELECT x FROM xa WHERE xmlagg(x) IS NULL","42803","aggregate functions are not allowed in WHERE","",24},
			{"SELECT xmlagg(x) FROM xa GROUP BY xmlagg(x)","42803","aggregate functions are not allowed in GROUP BY","",35},
			{"SELECT xmlagg(xmlagg(x)) FROM xa","42803","aggregate function calls cannot be nested","",15},
		}{xmlAggFailure(t,p,b,c,tc.q,xmlAggDiagnostic(tc.code,tc.msg,tc.hint,tc.pos),true)}
		d:=xmlAggDiagnostic("2200N","invalid XML content","",0)
		d.Detail="line 1: Premature end of data in tag a line 1\n<a>\n   ^"
		xmlAggFailure(t,p,b,c,"SELECT xmlagg(XMLPARSE(CONTENT $1)) AS v",d,false,"<a>")
		d.Position="19"
		xmlAggFailure(t,p,b,c,"SELECT xmlagg(XML '<a>') FROM xa",d,true)
	})
}
