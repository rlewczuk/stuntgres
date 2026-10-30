package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapRowsSmoke(t *testing.T){
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		baseline:="<table xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\">\n\n<row>\n  <a>1</a>\n  <b xsi:nil=\"true\"/>\n</row>\n\n</table>\n"
		step:=xmlSelect(`SELECT query_to_xml('SELECT 1 AS a,NULL::text AS b',true,false,'') AS v`,"XML",baseline)
		step.CheckMetadata=func(t *testing.T,r *sql.Rows){xmlMetadata(t,r,b.PG,false)}
		xmlBoth(t,p,b,c,step)
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xml('SELECT 1 AS a,NULL::text AS b',false,true,'urn:test') AS v`,"XML","<row xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\" xmlns=\"urn:test\">\n  <a>1</a>\n</row>\n\n"))
		xmlBoth(t,p,b,c,xmlSelect(`SELECT query_to_xml('SELECT 1 AS a',false,false,'a"b') AS v`,"XML",xmlMapDocument("table",`a"b`,xmlMapRow("  <a>1</a>\n"))))
		xmlMapExec(t,p,c,`CREATE TABLE xr_smoke(a int)`,`INSERT INTO xr_smoke VALUES(1)`)
		xmlBoth(t,p,b,c,xmlSelect(`SELECT table_to_xml('public.xr_smoke',false,false,'') AS v`,"XML",xmlMapDocument("xr_smoke","",xmlMapRow("  <a>1</a>\n"))))
		for _,tc:=range []struct{prefix,arg,root,suffix string}{
			{"query","SELECT 1 AS a","table",""},
			{"table","xr_smoke","xr_smoke","."+xmlMapDatabase(t,p,c)+".public.xr_smoke"},
		}{schema:=xmlMapIntSchema("a",tc.root,tc.suffix,"",false,false);xmlBoth(t,p,b,c,xmlSelect(`SELECT `+tc.prefix+`_to_xmlschema(`+xmlMapLiteral(tc.arg)+`,false,false,'') AS v`,"XML",schema));xmlBoth(t,p,b,c,xmlSelect(`SELECT `+tc.prefix+`_to_xml_and_xmlschema(`+xmlMapLiteral(tc.arg)+`,false,false,'') AS v`,"XML",xmlMapCombined(tc.root,"",schema,xmlMapRow("  <a>1</a>\n"),false)))}
		defer c.ExecContext(p.Ctx,`ROLLBACK`)
		for _,prepared:=range []bool{false,true}{
			xmlMapExec(t,p,c,`BEGIN`,`DECLARE xr_smoke_cursor SCROLL CURSOR FOR SELECT g AS x FROM generate_series(1,3) g`)
			// One held mapper is reused as the underlying real cursor advances.
			q:=`SELECT cursor_to_xml('xr_smoke_cursor',$1::integer,false,true,'') AS v`
			var held *sql.Stmt
			if prepared{var err error;held,err=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}}
			for i,count:=range []int{1,1,2,1}{want:="";if i<3{want=xmlMapForest("row","",fmt.Sprintf("  <x>%d</x>\n",i+1))};s:=xmlSelect(q,"XML",want);s.Args=[]any{count};var executor fixture.Executor=c;if held!=nil{executor=fixture.HeldStatement{Executor:c,Statement:held,SQL:q}};fixture.RunStep(t,p.Ctx,executor,b.Recorder,s);if i==0{xmlMapCursorStep(t,p,b,c,prepared,`SELECT cursor_to_xmlschema('xr_smoke_cursor',false,false,'') AS v`,"XML",xmlMapIntSchema("x","table","","",false,false))}}
			if held!=nil{held.Close()}
			xmlMapCursorStep(t,p,b,c,prepared,`SELECT cursor_to_xml('xr_smoke_cursor',1,false,false,'') AS v`,"XML",xmlMapDocument("table","",""))
			xmlMapExec(t,p,c,`CLOSE xr_smoke_cursor`,`ROLLBACK`)
		}
	})
}
