package reference_test

import (
 "database/sql"
 "testing"
 "github.com/jackc/pgx/v5/pgconn"
 "stuntgres/reference/fixture"
)

// Expectations are independent PostgreSQL18.6 PQprepare/Describe/Execute
// observations, including the retained UNKNOWN occurrence's character position.
func TestReferenceXMLProduceParameterPrecedence(t *testing.T){
 successes:=[]struct{q,label,want string;oid uint32;sibling bool}{
  {`SELECT xmlforest($1::integer AS a,$1 AS b)`,"xmlforest",`<a>7</a><b>7</b>`,23,false},
  {`SELECT xmlelement(NAME r,XMLATTRIBUTES($1::integer AS a,$1 AS b))`,"xmlelement",`<r a="7" b="7"/>`,23,false},
  {`SELECT xmlelement(NAME r,$1::integer,$1)`,"xmlelement",`<r>77</r>`,23,false},
  {`SELECT xmlelement(NAME r,XMLATTRIBUTES($1::integer AS a),$1)`,"xmlelement",`<r a="7">7</r>`,23,false},
  {`SELECT $1::integer AS typed,xmlforest($1 AS a)`,"xmlforest",`<a>7</a>`,23,true},
  {`SELECT $1::integer AS typed,xmlelement(NAME r,XMLATTRIBUTES($1 AS a))`,"xmlelement",`<r a="7"/>`,23,true},
  {`SELECT $1::integer AS typed,xmlelement(NAME r,$1)`,"xmlelement",`<r>7</r>`,23,true},
  {`SELECT xmlforest($1::text AS a,$1 AS b)`,"xmlforest",`<a>7</a><b>7</b>`,25,false},
 }
 xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  for _,tc:=range []struct{q,code,message string;position int}{
   {`SELECT xmlforest($1)`,"42601","unnamed XML element value must be a column reference",18},
   {`SELECT xmlelement(NAME r,XMLATTRIBUTES($1))`,"42601","unnamed XML attribute value must be a column reference",40},
   {`SELECT xmlforest($1,missing AS b)`,"42601","unnamed XML element value must be a column reference",18},
   {`SELECT xmlelement(NAME r,XMLATTRIBUTES($1,missing AS b))`,"42601","unnamed XML attribute value must be a column reference",40},
   {`SELECT $1::integer,xmlforest($1)`,"42601","unnamed XML element value must be a column reference",30},
   {`SELECT $1::integer,xmlelement(NAME r,XMLATTRIBUTES($1))`,"42601","unnamed XML attribute value must be a column reference",52},
   {`SELECT xmlforest($1 AS a, missing AS b)`,"42703",`column "missing" does not exist`,27},
   {`SELECT xmlelement(NAME r,XMLATTRIBUTES($1 AS a, missing AS b))`,"42703",`column "missing" does not exist`,49},
   {`SELECT xmlelement(NAME r,$1,missing)`,"42703",`column "missing" does not exist`,29},
   {`SELECT xmlforest($1 AS a),missing AS b`,"42703",`column "missing" does not exist`,27},
   {`SELECT xmlforest(missing AS a,$1 AS b)`,"42703",`column "missing" does not exist`,18},
   {`SELECT xmlforest($1 AS a)`,"42P18","could not determine data type of parameter $1",0},
   {`SELECT xmlelement(NAME r,XMLATTRIBUTES($1 AS a))`,"42P18","could not determine data type of parameter $1",0},
   {`SELECT xmlelement(NAME r,$1)`,"42P18","could not determine data type of parameter $1",0},
   {`SELECT xmlforest($1 AS a,$1::integer AS b)`,"42P08","could not determine data type of parameter $1",18},
   {`SELECT xmlelement(NAME r,XMLATTRIBUTES($1 AS a,$1::integer AS b))`,"42P08","could not determine data type of parameter $1",40},
   {`SELECT xmlelement(NAME r,$1,$1::integer)`,"42P08","could not determine data type of parameter $1",26},
   {`SELECT xmlelement(NAME r,XMLATTRIBUTES($1 AS a),$1::integer)`,"42P08","could not determine data type of parameter $1",40},
   {`SELECT xmlforest($1 AS a),$1::integer AS typed`,"42P08","could not determine data type of parameter $1",18},
   {`SELECT xmlelement(NAME r,XMLATTRIBUTES($1 AS a)),$1::integer AS typed`,"42P08","could not determine data type of parameter $1",40},
   {`SELECT xmlelement(NAME r,$1),$1::integer AS typed`,"42P08","could not determine data type of parameter $1",26},
   {`SELECT xmlforest($1 AS a,$1::text AS b)`,"42P08","could not determine data type of parameter $1",18},
   {`SELECT 'é' AS marker,xmlforest($1 AS a,$1::integer AS b)`,"42P08","could not determine data type of parameter $1",32},
  }{
   step:=fixture.Prepared(fixture.ErrorDiagnostic(tc.q,xmlError(tc.code,tc.message,"",tc.position)))
   step.SelectRows,step.ErrorStage=true,"prepare"
   fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
  }
  for _,tc:=range successes{
   columns,types,row:=[]string{tc.label},[]string{"XML"},[]any{tc.want}
   if tc.sibling{columns=append([]string{"typed"},columns...);types=append([]string{"INT4"},types...);row=append([]any{int64(7)},row...)}
   step:=fixture.Select(tc.q,columns,types,[][]any{row});step.Args=[]any{int64(7)};if tc.oid==25{step.Args=[]any{"7"}}
   xmlBoth(t,p,b,c,step)
  }
 })
 t.Run("wire",func(t *testing.T){
  f:=fixture.NewServerFixture(t)
  for _,b:=range f.Backends(){t.Run(b.Name,func(t *testing.T){
   c,err:=pgconn.Connect(f.Ctx,f.DSNFor(b));if err!=nil{t.Fatal(err)};defer c.Close(f.Ctx)
   for _,tc:=range successes{
    d,err:=c.Prepare(f.Ctx,"xml_parameter_precedence",tc.q,nil);if err!=nil{t.Fatal(tc.q,err)}
    if len(d.ParamOIDs)!=1||d.ParamOIDs[0]!=tc.oid{t.Fatal(tc.q,d.ParamOIDs)}
    count:=1;if tc.sibling{count=2};if len(d.Fields)!=count{t.Fatal(d.Fields)}
    field:=d.Fields[count-1]
    if field.Name!=tc.label||field.DataTypeOID!=142||field.DataTypeSize != -1||field.TypeModifier != -1||field.Format!=0||field.TableOID!=0||field.TableAttributeNumber!=0{t.Fatal(field)}
    if tc.sibling{field=d.Fields[0];if field.Name!="typed"||field.DataTypeOID!=23||field.DataTypeSize!=4||field.TypeModifier != -1||field.Format!=0||field.TableOID!=0||field.TableAttributeNumber!=0{t.Fatal(field)}}
    for range 2{
     result:=c.ExecPrepared(f.Ctx,"xml_parameter_precedence",[][]byte{[]byte("7")},nil,nil).Read()
     if result.Err!=nil||len(result.Rows)!=1||len(result.Rows[0])!=count{t.Fatal(result)}
     if string(result.Rows[0][count-1])!=tc.want||tc.sibling&&string(result.Rows[0][0])!="7"{t.Fatal(result.Rows)}
    }
    if _,err:=c.Exec(f.Ctx,"DEALLOCATE xml_parameter_precedence").ReadAll();err!=nil{t.Fatal(err)}
   }
  })}
 })
}
