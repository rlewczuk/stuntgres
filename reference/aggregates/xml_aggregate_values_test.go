package reference_test

import (
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLAggValues(t *testing.T) {
	xmlAggScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		check := func(q string, values ...any) { xmlAggBoth(t,p,b,c,xmlAggSelect(q,"XML",values...)) }
		run(fixture.Exec("CREATE TABLE xa(k int,x xml)",0))
		xmlAggBoth(t,p,b,c,fixture.Select("SELECT xmlagg(x) FROM xa",[]string{"xmlagg"},[]string{"XML"},[][]any{{nil}}))
		check("SELECT xmlagg(x) AS v FROM xa",nil)
		xmlAggBoth(t,p,b,c,fixture.Select("SELECT k,xmlagg(x) AS v FROM xa GROUP BY k",[]string{"k","v"},[]string{"INT4","XML"},[][]any{}))
		run(fixture.Exec("INSERT INTO xa VALUES(1,NULL),(1,NULL)",2))
		check("SELECT xmlagg(x) AS v FROM xa",nil)
		xmlAggBoth(t,p,b,c,fixture.Select("SELECT k,xmlagg(x) AS v FROM xa GROUP BY k",[]string{"k","v"},[]string{"INT4","XML"},[][]any{{int64(1),nil}}))
		run(fixture.Exec("INSERT INTO xa VALUES(2,XMLPARSE(CONTENT ''))",1))
		check("SELECT xmlagg(x) AS v FROM xa","")
		run(fixture.Exec("DELETE FROM xa",3))
		run(fixture.Exec("INSERT INTO xa VALUES(1,'<a/>'),(2,NULL),(3,'text<b/>'),(4,'<!--c-->')",4))
		check("SELECT xmlagg(x ORDER BY k) AS v FROM xa","<a/>text<b/><!--c-->")
		check("SELECT xmlagg(x ORDER BY k) FILTER(WHERE false) AS v FROM xa",nil)
		check("SELECT xmlagg(x ORDER BY k) FILTER(WHERE k > 2) AS v FROM xa","text<b/><!--c-->")
		run(fixture.Exec("SET XML OPTION DOCUMENT",0))
		check("SELECT xmlagg(x ORDER BY k) AS v FROM xa","<a/>text<b/><!--c-->")
		run(fixture.Exec("SET XML OPTION CONTENT",0))
		run(fixture.Exec("DELETE FROM xa",4))
		run(fixture.Exec("INSERT INTO xa VALUES(1,'<a/>'),(2,'<a/>')",2))
		check("SELECT xmlagg(x) AS v FROM xa","<a/><a/>")
		check("SELECT xmlagg(NULL) AS v FROM xa",nil)
		run(fixture.Exec("DELETE FROM xa",2))
		run(fixture.Exec("INSERT INTO xa VALUES(1,' &amp; <a/><b/> '),(2,'&#65;')",2))
		check("SELECT xmlagg(x ORDER BY k) AS v FROM xa"," &amp; <a/><b/> &#65;")
	})
}
