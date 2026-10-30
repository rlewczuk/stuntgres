package reference_test

import (
 "bytes"
 "database/sql"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceXMLProduceSmoke(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,tc:=range []struct{q string;want any}{
  {`SELECT xmltext('<&>"') AS v`,`&lt;&amp;&gt;&quot;`},
  {`SELECT xmlcomment('hello') AS v`,`<!--hello-->`},
  {`SELECT xmlconcat('<?xml version="1.1" standalone="yes"?><a/>',NULL,'<?xml version="1.1" standalone="no"?><b/>') AS v`,`<?xml version="1.1" standalone="no"?><a/><b/>`},
  {`SELECT xmlelement(NAME r,XMLATTRIBUTES('é😀<&"' AS a),xml '<x/>') AS v`,`<r a="&#xE9;&#x1F600;&lt;&amp;&quot;"><x/></r>`},
  {`SELECT xmlforest(1 AS a,NULL AS b,'<&' AS c) AS v`,`<a>1</a><c>&lt;&amp;</c>`},
  {`SELECT xmlpi(NAME p) AS v`,`<?p?>`},{`SELECT xmlpi(NAME p,'') AS v`,`<?p ?>`},{`SELECT xmlpi(NAME p,NULL) AS v`,nil},
  {`SELECT xmlroot('<?xml version="1.0" encoding="UTF8"?><x/>',VERSION '1.1',STANDALONE YES) AS v`,`<?xml version="1.1" standalone="yes"?><x/>`},
  {`SELECT xmlforest(content) AS v FROM (SELECT 'x'::text AS content)s`,`<content>x</content>`},
  {`SELECT xmlconcat(document) AS v FROM (SELECT '<a/>'::xml AS document)s`,`<a/>`},
  {`SELECT xmlroot(document,VERSION '1.0') AS v FROM (SELECT '<a/>'::xml AS document)s`,`<a/>`},
 }{xmlBoth(t,p,b,c,xmlSelect(tc.q,"XML",tc.want))}
 xmlFailure(t,p,b,c,`SELECT xmlcomment('a--b') WHERE false`,xmlError("2200S","invalid XML comment","",0),false)
 xmlFailure(t,p,b,c,`SELECT xmlpi(NAME p,'a?>b')`,xmlError("2200T","invalid XML processing instruction",`XML processing instruction cannot contain "?>".`,0),false)
 xmlFailure(t,p,b,c,`SELECT xmlelement(NAME r,XMLATTRIBUTES(NULL AS "foo$bar",2 AS "foo$bar"))`,xmlError("42601",`XML attribute name "foo_x0024_bar" appears more than once`,"",58),true)
 xmlBoth(t,p,b,c,xmlSelect(`SELECT xmlpi(NAME "XML",'a?>b') AS v WHERE false`,"XML"))
 q:=`SELECT xmlcomment($1) AS v`;stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
 for _,value:=range []any{"healthy",nil,""}{rows,err:=stmt.QueryContext(p.Ctx,value);if err!=nil{t.Fatal(err)};xmlMetadata(t,rows,b.PG,false);if !rows.Next(){t.Fatal(rows.Err())};var got sql.NullString;if err:=rows.Scan(&got);err!=nil{t.Fatal(err)};if value==nil{if got.Valid{t.Fatal(got)}}else if !got.Valid||got.String!="<!--"+value.(string)+"-->"{t.Fatal(got)};rows.Close()}
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`CREATE TABLE xml_produce_smoke(id integer,v xml)`,0));fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`INSERT INTO xml_produce_smoke VALUES(9,xmlelement(NAME sentinel))`,1))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.ErrorDiagnostic(`INSERT INTO xml_produce_smoke SELECT id,xmlcomment(t) FROM (VALUES(1,'good'),(2,'a--b'))s(id,t)`,xmlError("2200S","invalid XML comment","",0)))
 want:=fixture.Select(`SELECT id,v FROM xml_produce_smoke ORDER BY id`,[]string{"id","v"},[]string{"INT4","XML"},[][]any{{int64(9),`<sentinel/>`}});xmlBoth(t,p,b,c,want)
 good:=xmlSelect(q,"XML",`<!--healthy-->`);good.Args=[]any{"healthy"};fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,good)
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`CREATE VIEW xml_produce_smoke_view AS SELECT xmlelement(NAME r,xmlcomment('ok')) AS v`,0))
 xmlProduceNameReplay(t,p,b,c,"xml_smoke_name","t",true,"t","t")
 if b.PG{return}
 old,err:=c.QueryContext(p.Ctx,`SELECT v FROM xml_produce_smoke`);if err!=nil{t.Fatal(err)};defer old.Close();xmlMetadata(t,old,false,true)
 var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)};if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)};if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
 xmlMetadata(t,old,false,true);if !old.Next(){t.Fatal(old.Err())};var retained string;if err:=old.Scan(&retained);err!=nil||retained!=`<sentinel/>`{t.Fatal(retained,err)};old.Close()
 fresh,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer fresh.Close();xmlBoth(t,p,b,fresh,want);xmlBoth(t,p,b,fresh,xmlSelect(`SELECT v FROM xml_produce_smoke_view`,"XML",`<r><!--ok--></r>`))
})}
