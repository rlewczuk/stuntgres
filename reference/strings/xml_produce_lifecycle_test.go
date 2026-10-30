package reference_test

import (
 "bytes"
 "database/sql"
 "reflect"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceXMLProduceLifecycle(t *testing.T){
 fixture.SchemaReferenceCleanup(t,"xml_produce_schema","xml_produce_schema_renamed")
 xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  for _,q:=range []string{
   `CREATE TABLE xml_produce_data(id integer,t text,x xml)`,
   `INSERT INTO xml_produce_data VALUES(1,'<&','<a/>'),(2,'','<b/>')`,
   `CREATE VIEW xml_produce_view AS SELECT id,xmltext(t) AS tx,xmlcomment(t) AS cm,xmlconcat(CAST(x AS xml),'<end/>') AS co,xmlelement(NAME r,XMLATTRIBUTES(CAST(t AS text) AS a),CAST(x AS xml),CAST(t AS text)) AS el,xmlforest(xmltext(t) AS f,CAST(x AS xml) AS x) AS fo,xmlpi(NAME p,CAST(t AS text)) AS pi,xmlroot(xmlconcat(x,''::xml),VERSION CAST('1.1' AS text),STANDALONE YES) AS ro FROM xml_produce_data`,
  }{if _,err:=c.ExecContext(p.Ctx,q);err!=nil{t.Fatal(err)}}
  expected:=fixture.Select(`SELECT id,tx,cm,co,el,fo,pi,ro FROM xml_produce_view ORDER BY id`,[]string{"id","tx","cm","co","el","fo","pi","ro"},[]string{"INT4","XML","XML","XML","XML","XML","XML","XML"},[][]any{
   {int64(1),`&lt;&amp;`,`<!--<&-->`,`<a/><end/>`,`<r a="&lt;&amp;"><a/>&lt;&amp;</r>`,`<f>&lt;&amp;</f><x><a/></x>`,`<?p <&?>`,`<?xml version="1.1" standalone="yes"?><a/>`},
   {int64(2),"",`<!---->`,`<b/><end/>`,`<r a=""><b/></r>`,`<f></f><x><b/></x>`,`<?p ?>`,`<?xml version="1.1" standalone="yes"?><b/>`},
  });xmlBoth(t,p,b,c,expected)
  replay:=func(name string){t.Helper();var definition string;if err:=c.QueryRowContext(p.Ctx,`SELECT pg_get_viewdef('xml_produce_view'::regclass,true)`).Scan(&definition);err!=nil{t.Fatal(err)};fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`CREATE VIEW `+name+` AS `+definition,0));want:=expected;want.Query=`SELECT id,tx,cm,co,el,fo,pi,ro FROM `+name+` ORDER BY id`;xmlBoth(t,p,b,c,want)}
  replay("xml_produce_replay")
  for _,q:=range []string{`ALTER TABLE xml_produce_data RENAME COLUMN t TO renamed_text`,`ALTER TABLE xml_produce_data RENAME TO xml_produce_renamed`,`CREATE SCHEMA xml_produce_schema`,`ALTER TABLE xml_produce_renamed SET SCHEMA xml_produce_schema`,`ALTER SCHEMA xml_produce_schema RENAME TO xml_produce_schema_renamed`}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
  xmlBoth(t,p,b,c,expected);replay("xml_produce_qualified_replay")
  if b.PG{return}
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`CREATE TABLE xml_produce_saved(v xml)`,0));fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`INSERT INTO xml_produce_saved VALUES(xmlelement(NAME saved,XMLATTRIBUTES('é' AS a),xmlcomment('ok')))`,1))
  old,err:=c.QueryContext(p.Ctx,`SELECT v FROM xml_produce_saved`);if err!=nil{t.Fatal(err)};defer old.Close();xmlMetadata(t,old,false,true)
  var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)};if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)};if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
  xmlMetadata(t,old,false,true);if !old.Next(){t.Fatal(old.Err())};var retained string;if err:=old.Scan(&retained);err!=nil||retained!=`<saved a="&#xE9;"><!--ok--></saved>`{t.Fatal(retained,err)};if old.Next()||old.Err()!=nil{t.Fatal(old.Err())};old.Close()
  xmlBoth(t,p,b,c,expected)
  fresh,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer fresh.Close();xmlBoth(t,p,b,fresh,xmlSelect(`SELECT v FROM xml_produce_saved`,"XML",`<saved a="&#xE9;"><!--ok--></saved>`))
  xmlGeneratedReplayFailure(t,p,b,c)
 })
 t.Run("cast-identity",func(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  for _,q:=range []string{`CREATE FUNCTION xml_produce_lifecycle_cast(integer) RETURNS xml LANGUAGE sql IMMUTABLE AS 'SELECT XMLPARSE(CONTENT ''<cast/>'')'`,`CREATE CAST (integer AS xml) WITH FUNCTION xml_produce_lifecycle_cast(integer) AS ASSIGNMENT`,`CREATE VIEW xml_produce_cast_view AS SELECT xmlconcat(NULL::integer) AS v`}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
  defer func(){if _,err:=c.ExecContext(p.Ctx,`DROP CAST IF EXISTS (integer AS xml)`);err!=nil{t.Error(err)}}()
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`ALTER FUNCTION xml_produce_lifecycle_cast(integer) RENAME TO xml_produce_lifecycle_renamed`,0));xmlBoth(t,p,b,c,xmlSelect(`SELECT v FROM xml_produce_cast_view`,"XML",`<cast/>`))
  var definition string;if err:=c.QueryRowContext(p.Ctx,`SELECT pg_get_viewdef('xml_produce_cast_view'::regclass,true)`).Scan(&definition);err!=nil{t.Fatal(err)};fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`CREATE VIEW xml_produce_cast_replay AS `+definition,0));xmlBoth(t,p,b,c,xmlSelect(`SELECT v FROM xml_produce_cast_replay`,"XML",`<cast/>`))
 })})
}

// This native phase crosses the real generated-value/literal-input boundary.
// The emitted source dump is never patched into a synthetic malformed script.
func xmlGeneratedReplayFailure(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 t.Helper()
 const raw=`<?xml version="x" y"?><a/>`
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`CREATE TABLE xml_produce_nonreinputtable(v xml)`,0))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`INSERT INTO xml_produce_nonreinputtable VALUES(xmlroot('<a/>',VERSION 'x" y'))`,1))
 source:=xmlSelect(`SELECT v::text AS v FROM xml_produce_nonreinputtable`,"TEXT",raw)
 xmlBoth(t,p,b,c,source)
 var notices []fixture.Diagnostic
 noticePool,noticeConn,noticeRecorder:=fixture.NoticeReferencePool(t,p,false,&notices)
 defer noticePool.Close();defer noticeConn.Close()
 retained,err:=noticeConn.QueryContext(p.Ctx,`SELECT v FROM xml_produce_nonreinputtable`);if err!=nil{t.Fatal(err)};defer retained.Close()
 xmlMetadata(t,retained,false,true)
 var emitted bytes.Buffer
 if err:=stuntgres.Dump(p.Ctx,p.Native,&emitted);err!=nil{t.Fatal(err)}
 if !bytes.Contains(emitted.Bytes(),[]byte(`XMLPARSE(CONTENT '`+raw+`')`)){t.Fatal("dump lost actual generated spelling/mode",emitted.String())}
 driver:=stuntgres.NewDriver();connector,err:=driver.OpenConnector("xml-produce-malformed-destination/"+t.Name());if err!=nil{t.Fatal(err)}
 destinationNative:=sql.OpenDB(connector);defer destinationNative.Close()
 destination,recorder:=fixture.RecordedNativePool(t,connector)
 for _,step:=range []fixture.Step{
  fixture.Exec(`CREATE TABLE xml_produce_destination(id integer,v xml)`,0),
  fixture.Exec(`INSERT INTO xml_produce_destination VALUES(91,'<destination/>')`,1),
  fixture.Exec(`CREATE VIEW xml_produce_destination_view AS SELECT id,v FROM xml_produce_destination`,0),
 }{fixture.RunStep(t,p.Ctx,destination,recorder,step)}
 checkDestination:=func(){
  t.Helper()
  fixture.RunStep(t,p.Ctx,destination,recorder,fixture.Select(`SELECT id,v FROM xml_produce_destination_view`,[]string{"id","v"},[]string{"INT4","XML"},[][]any{{int64(91),`<destination/>`}}))
  rows,err:=destination.QueryContext(p.Ctx,`SELECT v FROM xml_produce_destination`);if err!=nil{t.Fatal(err)};defer rows.Close()
  xmlMetadata(t,rows,false,true)
  if !rows.Next(){t.Fatal(rows.Err())};var got string;if err:=rows.Scan(&got);err!=nil||got!=`<destination/>`{t.Fatal(got,err)}
  if rows.Next()||rows.Err()!=nil{t.Fatal(rows.Err())}
 }
 checkDestination()
 var before bytes.Buffer
 if err:=stuntgres.Dump(p.Ctx,destinationNative,&before);err!=nil{t.Fatal(err)}
 diagnostic:=xmlError("2200N","invalid XML content: invalid XML declaration","Parsing XML declaration: '?>' expected.",0)
 fixture.AssertDiagnostic(t,stuntgres.Restore(p.Ctx,destinationNative,bytes.NewReader(emitted.Bytes())),&diagnostic)
 checkDestination()
 var after bytes.Buffer
 if err:=stuntgres.Dump(p.Ctx,destinationNative,&after);err!=nil{t.Fatal(err)}
 if !bytes.Equal(before.Bytes(),after.Bytes()){t.Fatal("failed generated-value replay changed destination catalog/rows")}
 xmlMetadata(t,retained,false,true)
 if !retained.Next(){t.Fatal(retained.Err())};var got string
 if err:=retained.Scan(&got);err!=nil||got!=raw{t.Fatal("retained generated source",got,err)}
 if retained.Next()||retained.Err()!=nil{t.Fatal(retained.Err())};retained.Close()
 warning:=fixture.Diagnostic{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:"XX001",Message:"could not parse XML declaration in stored value",Detail:"Parsing XML declaration: '?>' expected."}
 if !reflect.DeepEqual(notices,[]fixture.Diagnostic{warning}){t.Fatal("actual retained XML output warning",notices)}
 fixture.RunStep(t,p.Ctx,noticeConn,noticeRecorder,source)
 fixture.RunStep(t,p.Ctx,noticeConn,noticeRecorder,fixture.Prepared(source))
 if !reflect.DeepEqual(notices,[]fixture.Diagnostic{warning}){t.Fatal("raw source read emitted a warning",notices)}
 var sourceAfter bytes.Buffer
 if err:=stuntgres.Dump(p.Ctx,p.Native,&sourceAfter);err!=nil{t.Fatal(err)}
 if !bytes.Equal(emitted.Bytes(),sourceAfter.Bytes()){t.Fatal("destination replay failure changed generated source")}
}
