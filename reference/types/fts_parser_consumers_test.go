package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSParserConsumers(t *testing.T) {
	for _, prepared := range []bool{false,true} {
		t.Run(fmt.Sprint(prepared),func(t *testing.T) {
			ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
				run(fixture.Exec("CREATE TABLE fts_parser_input(id integer,parser text,txt text)",0))
				run(fixture.Exec("INSERT INTO fts_parser_input VALUES(1,'default','Word'),(2,'missing','Late')",2))
				run(fixture.Exec("CREATE TABLE fts_parser_output(n bigint,tokid integer,token text)",0))
				run(fixture.Exec("INSERT INTO fts_parser_output VALUES(0,1,'sentinel')",1))
				query := "INSERT INTO fts_parser_output SELECT p.ordinality,p.tokid,p.token FROM (SELECT * FROM fts_parser_input ORDER BY id OFFSET 0) i CROSS JOIN LATERAL ts_parse(i.parser,i.txt) WITH ORDINALITY p"
				s := fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("42704",`text search parser "missing" does not exist`))
				s.Prepared,s.ErrorStage=prepared,"execute"
				run(s)
				ftsParserSelect(t,p,b,"SELECT n,tokid,token FROM fts_parser_output ORDER BY n",[]string{"n","tokid","token"},[]string{"INT8","INT4","TEXT"},[][]any{{int64(0),int64(1),"sentinel"}})
				query = "CREATE TABLE fts_parser_failed AS SELECT p.* FROM (SELECT * FROM fts_parser_input ORDER BY id OFFSET 0) i CROSS JOIN LATERAL ts_parse(i.parser,i.txt) p"
				s = fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("42704",`text search parser "missing" does not exist`))
				s.Prepared,s.ErrorStage=prepared,"execute"
				run(s)
				ftsParserSelect(t,p,b,"SELECT count(*) FROM pg_class WHERE relname='fts_parser_failed' AND relnamespace='public'::regnamespace",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}})
				run(fixture.Exec("DELETE FROM fts_parser_input WHERE id=2",1))
				s = fixture.Exec("INSERT INTO fts_parser_output SELECT p.ordinality,p.tokid,p.token FROM fts_parser_input i CROSS JOIN LATERAL ts_parse(i.parser,i.txt) WITH ORDINALITY p",1)
				s.Prepared=prepared
				run(s)
				ftsParserSelect(t,p,b,"SELECT n,tokid,token FROM fts_parser_output ORDER BY n",[]string{"n","tokid","token"},[]string{"INT8","INT4","TEXT"},[][]any{{int64(0),int64(1),"sentinel"},{int64(1),int64(1),"Word"}})
			})
		})
	}
}
