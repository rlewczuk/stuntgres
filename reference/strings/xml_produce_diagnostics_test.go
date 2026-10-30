package reference_test

import (
 "database/sql"
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceXMLProduceDiagnostics(t *testing.T){
 xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  for _,tc:=range []struct{q,code,message,detail string;pos int;analysis bool}{
   {`SELECT xmlelement(NAME r,XMLATTRIBUTES('x'))`,"42601","unnamed XML attribute value must be a column reference","",40,true},
   {`SELECT xmlforest('x')`,"42601","unnamed XML element value must be a column reference","",18,true},
   {`SELECT 'é' AS marker, xmlforest('x')`,"42601","unnamed XML element value must be a column reference","",33,true},
   {`SELECT xmlelement(NAME r,XMLATTRIBUTES(NULL AS a,2 AS a))`,"42601",`XML attribute name "a" appears more than once`,"",50,true},
   {`SELECT xmlelement(NAME r,XMLATTRIBUTES(NULL AS "foo$bar",2 AS "foo$bar"))`,"42601",`XML attribute name "foo_x0024_bar" appears more than once`,"",58,true},
   {`SELECT xmlconcat(1,2)`,"42804","argument of XMLCONCAT must be type xml, not type integer","",18,true},
   {`SELECT xmlroot(1,VERSION '1.0')`,"42804","argument of XMLROOT must be type xml, not type integer","",16,true},
   {`SELECT xmlconcat('<a/>'::text)`,"42804","argument of XMLCONCAT must be type xml, not type text","",18,true},
   {`SELECT xmlconcat('<a/>','<wrong></syntax>  ')`,"2200N","invalid XML content","line 1: Opening and ending tag mismatch: wrong line 1 and syntax\n<wrong></syntax>  \n                ^",25,true},
   {`SELECT xmlforest(missing+1)`,"42703",`column "missing" does not exist`,"",18,true},
   {`SELECT xmlroot(NULL,VERSION (1/0)::text)`,"22012","division by zero","",0,false},
   {`SELECT xmlpi(NAME "XML",(1/0)::text)`,"22012","division by zero","",0,false},
   {`SELECT xmlroot(NULL,VERSION (1/0)::text) WHERE false`,"22012","division by zero","",0,false},
   {`SELECT xmlelement(NAME r,XMLATTRIBUTES('infinity'::date AS a),(1/0)::text)`,"22012","division by zero","",0,false},
   {`SELECT xmlforest('infinity'::date AS d,(1/0)::text AS x)`,"22012","division by zero","",0,false},
  }{xmlFailure(t,p,b,c,tc.q,xmlError(tc.code,tc.message,tc.detail,tc.pos),tc.analysis)}
  // Bare unresolved parameters are a Prepare contract. A no-argument
  // database/sql call is not PostgreSQL's simple-query protocol operation.
  for _,q:=range []string{`SELECT xmlforest($1 AS a)`,`SELECT xmlelement(NAME r,$1)`,`SELECT $1 IS NULL`}{
   prepared:=fixture.Prepared(fixture.ErrorDiagnostic(q,xmlError("42P18","could not determine data type of parameter $1","",0)));prepared.SelectRows,prepared.ErrorStage=true,"prepare"
   fixture.RunStep(t,p.Ctx,c,b.Recorder,prepared)
  }
  inferred:=xmlSelect(`SELECT $1::integer AS v`,"INT4",int64(7));inferred.Args=[]any{7}
  xmlBoth(t,p,b,c,inferred)
  d:=xmlError("2200N","invalid XML content","line 1: Premature end of data in tag bad line 1\n<bad>\n     ^",0);d.Context="unnamed portal parameter $1 = '...'"
  xmlFailure(t,p,b,c,`SELECT xmlconcat($1,'<b/>')`,d,false,"<bad>")
  qualified:=xmlError("42883","function pg_catalog.xmlconcat(unknown, unknown) does not exist","",8);qualified.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
  xmlFailure(t,p,b,c,`SELECT pg_catalog.xmlconcat('<a/>','<b/>')`,qualified,true)
  for _,q:=range []string{`SELECT xmlelement(NAME r,'infinity'::date) AS v WHERE false`,`SELECT xmlforest('infinity'::date AS d) AS v WHERE false`,`SELECT xmlroot('<a/>',VERSION 'x" y') AS v WHERE false`}{xmlBoth(t,p,b,c,xmlSelect(q,"XML"))}
  for _,q:=range []string{`CREATE FUNCTION xml_produce_source(integer) RETURNS xml LANGUAGE sql IMMUTABLE AS 'SELECT XMLPARSE(CONTENT ''<cast/>'')'`,`CREATE CAST (integer AS xml) WITH FUNCTION xml_produce_source(integer)`}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
  defer func(){if _,err:=c.ExecContext(p.Ctx,`DROP CAST IF EXISTS (integer AS xml)`);err!=nil{t.Error(err)}}()
  xmlFailure(t,p,b,c,`SELECT xmlconcat(NULL::integer)`,xmlError("42804","argument of XMLCONCAT must be type xml, not type integer","",18),true)
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`DROP CAST (integer AS xml)`,0))
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`CREATE CAST (integer AS xml) WITH FUNCTION xml_produce_source(integer) AS ASSIGNMENT`,0))
  xmlBoth(t,p,b,c,xmlSelect(`SELECT xmlconcat(NULL::integer) AS v`,"XML",`<cast/>`))
  xmlBoth(t,p,b,c,xmlSelect(`SELECT xmlroot(NULL::integer,VERSION '1.0') AS v`,"XML",`<cast/>`))
 })
 t.Run("notices",xmlProduceNotices)
}

func xmlProduceNotices(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,_ *sql.Conn){
 var notices []fixture.Diagnostic
 _,c,rec:=fixture.NoticeReferencePool(t,p,b.PG,&notices)
 warning:=xmlError("XX001","could not parse XML declaration in stored value","Parsing XML declaration: '?>' expected.",0);warning.Severity,warning.SeverityUnlocalized="WARNING","WARNING"
 check:=func(want ...fixture.Diagnostic){t.Helper();if len(notices)!=len(want)||len(want)>0&&!reflect.DeepEqual(notices,want){t.Fatalf("notices got %#v want %#v",notices,want)};notices=nil}
 bad:=`<?xml version="x" y"?><a/>`
 for _,tc:=range []struct{q,want,typ string;warn bool}{
  {`SELECT xmlroot('<a/>',VERSION 'x" y') AS v`,bad,"XML",true},
  {`SELECT xmlroot('<a/>',VERSION 'x" y')::text AS v`,bad,"TEXT",false},
  {`SELECT xmlelement(NAME r,xmlroot('<a/>',VERSION 'x" y')) AS v`,`<r>`+bad+`</r>`,"XML",true},
  {`SELECT xmlconcat(xmlroot('<a/>',VERSION 'x" y')) AS v`,`<?xml version="x"?>`,"XML",false},
 }{
  stmt,err:=c.PrepareContext(p.Ctx,tc.q);if err!=nil{t.Fatal(err)};check()
  for range 2{fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:tc.q},rec,xmlSelect(tc.q,tc.typ,tc.want));if tc.warn{check(warning)}else{check()}}
  stmt.Close()
 }
 nonASCII:=warning;nonASCII.Detail="Invalid character value."
 q:=`SELECT xmlroot('<a/>',VERSION 'é') AS v`
 stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};check()
 for range 2{fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},rec,xmlSelect(q,"XML",`<?xml version="é"?><a/>`));check(nonASCII)};stmt.Close()
 fixture.RunStep(t,p.Ctx,c,rec,xmlSelect(`SELECT xmlroot('<a/>',VERSION 'x" y') AS v WHERE false`,"XML"));check()
})}
