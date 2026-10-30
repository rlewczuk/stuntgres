package reference_test

import (
 "context"
 "database/sql"
 "errors"
 "testing"
 "github.com/jackc/pgx/v5/pgconn"
 "stuntgres/reference/fixture"
)

func TestReferenceXMLProduceMetadata(t *testing.T){
 xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  for _,tc:=range []struct{label,expression string}{
   {"xmltext",`xmltext('x')`},{"xmlcomment",`xmlcomment('x')`},{"xmlconcat",`xmlconcat('<x/>')`},{"xmlelement",`xmlelement(NAME r)`},{"xmlforest",`xmlforest(1 AS a)`},{"xmlpi",`xmlpi(NAME r)`},{"xmlroot",`xmlroot('<x/>',VERSION '1.0')`},
  }{
   q:=`SELECT `+tc.expression+` WHERE false`;for _,prepared:=range []bool{false,true}{var rows *sql.Rows;var err error;if prepared{stmt,e:=c.PrepareContext(p.Ctx,q);if e!=nil{t.Fatal(e)};defer stmt.Close();rows,err=stmt.QueryContext(p.Ctx)}else{rows,err=c.QueryContext(p.Ctx,q)};if err!=nil{t.Fatal(err)};columns,err:=rows.Columns();if err!=nil||len(columns)!=1||columns[0]!=tc.label{t.Fatal(columns,err)};types,err:=rows.ColumnTypes();if err!=nil||types[0].DatabaseTypeName()!="XML"{t.Fatal(types,err)};if rows.Next()||rows.Err()!=nil{t.Fatal(rows.Err())};rows.Close()}
  }
  q:=`SELECT xmlelement(NAME r,$1::text) AS v`;stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
  for _,value:=range []any{"<&",nil,""}{rows,err:=stmt.QueryContext(p.Ctx,value);if err!=nil{t.Fatal(err)};xmlMetadata(t,rows,b.PG,false);if !rows.Next(){t.Fatal(rows.Err())};var got string;if err:=rows.Scan(&got);err!=nil{t.Fatal(err)};want:=`<r/>`;if value=="<&"{want=`<r>&lt;&amp;</r>`}else if value==""{want=`<r></r>`};if got!=want{t.Fatal(got,want)};if rows.Next()||rows.Err()!=nil{t.Fatal(rows.Err())};rows.Close()}
 })
 t.Run("wire",func(t *testing.T){
  f:=fixture.NewServerFixture(t)
  for _,b:=range f.Backends(){t.Run(b.Name,func(t *testing.T){
   c,err:=pgconn.Connect(f.Ctx,f.DSNFor(b));if err!=nil{t.Fatal(err)};defer c.Close(f.Ctx)
   for _,tc:=range []struct{q string;parameter uint32}{
    {`SELECT xmltext($1) AS v`,25},{`SELECT xmlcomment($1) AS v`,25},
    {`SELECT xmlelement(NAME r,$1::text) AS v`,25},{`SELECT xmlconcat($1) AS v`,142},
    {`SELECT xmlforest($1::text AS a) AS v`,25},{`SELECT xmlpi(NAME r,$1) AS v`,25},
    {`SELECT xmlroot($1,VERSION '1.0') AS v`,142},
   }{
    description,err:=c.Prepare(f.Ctx,"xml_produce_description",tc.q,nil);if err!=nil{t.Fatal(err)}
    xmlWireField(t,description.Fields,142,-1,-1,0)
    if len(description.ParamOIDs)!=1||description.ParamOIDs[0]!=tc.parameter{t.Fatal(description.ParamOIDs)}
    if _,err:=c.Exec(f.Ctx,"DEALLOCATE xml_produce_description").ReadAll();err!=nil{t.Fatal(err)}
   }
  })}
 })
}

func TestReferenceXMLProduceAtomicityCancellation(t *testing.T){xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,s:=range []fixture.Step{fixture.Exec(`CREATE TABLE xml_produce_atomic(id integer,v xml)`,0),fixture.Exec(`INSERT INTO xml_produce_atomic VALUES(9,'<sentinel/>')`,1)}{fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 sentinel:=fixture.Select(`SELECT id,v FROM xml_produce_atomic ORDER BY id`,[]string{"id","v"},[]string{"INT4","XML"},[][]any{{int64(9),`<sentinel/>`}})
 for _,tc:=range []struct{expression,code,message,detail string}{
  {`xmlcomment(CASE WHEN id=1 THEN 'good' ELSE 'a--b' END)`,"2200S","invalid XML comment",""},
  {`xmlpi(NAME p,CASE WHEN id=1 THEN 'good' ELSE 'a?>b' END)`,"2200T","invalid XML processing instruction",`XML processing instruction cannot contain "?>".`},
  {`xmlelement(NAME d,CASE WHEN id=1 THEN DATE '2020-01-01' ELSE 'infinity'::date END)`,"22008","date out of range","XML does not support infinite date values."},
 }{
  q:=`INSERT INTO xml_produce_atomic SELECT id,`+tc.expression+` FROM (VALUES(1),(2))s(id)`;fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.ErrorDiagnostic(q,xmlError(tc.code,tc.message,tc.detail,0)));xmlBoth(t,p,b,c,sentinel)
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`INSERT INTO xml_produce_atomic VALUES(1,'<a/>'),(2,'<b/>')`,2))
  q=`UPDATE xml_produce_atomic SET v=`+tc.expression+` WHERE id IN (1,2)`;fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.ErrorDiagnostic(q,xmlError(tc.code,tc.message,tc.detail,0)))
  xmlBoth(t,p,b,c,fixture.Select(`SELECT id,v FROM xml_produce_atomic ORDER BY id`,[]string{"id","v"},[]string{"INT4","XML"},[][]any{{int64(1),`<a/>`},{int64(2),`<b/>`},{int64(9),`<sentinel/>`}}))
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`DELETE FROM xml_produce_atomic WHERE id IN (1,2)`,2))
 }
 q:=`SELECT xmlcomment($1) AS v`;stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
 if !b.PG{
  canceled,cancel:=context.WithCancel(p.Ctx);cancel()
  for _,prepared:=range []bool{false,true}{var rows *sql.Rows;var err error;if prepared{rows,err=stmt.QueryContext(canceled,"good")}else{rows,err=c.QueryContext(canceled,q,"good")};if rows!=nil{rows.Close()};if !errors.Is(err,context.Canceled){t.Fatal("native precancel",err)}}
  _,err=c.ExecContext(canceled,`INSERT INTO xml_produce_atomic VALUES(7,xmlcomment('good'))`);if !errors.Is(err,context.Canceled){t.Fatal(err)}
 }
 good:=xmlSelect(q,"XML",`<!--healthy-->`);good.Args=[]any{"healthy"};fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,good);xmlBoth(t,p,b,c,sentinel)
})}
