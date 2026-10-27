package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceHstoreJSONBNumericBoundaries(t *testing.T){
 for _,tc:=range []struct{number,output string}{{"1e131071","1"+strings.Repeat("0",131071)},{"1e-16383","0."+strings.Repeat("0",16382)+"1"}}{
  t.Run(tc.number,func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   hstoreInstall(t,p,b,c);q:="SELECT hstore_to_jsonb_loose($1::hstore) AS j"
   s:=hstoreJSONBSelect(b,q,[]string{"j"},[][]any{{`{"a": `+tc.output+`}`}},false,"a=>"+tc.number)
   hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s))
  })})
 }
 for _,number:=range []string{"1e131072","1e-16384"}{t.Run(number,func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  hstoreInstall(t,p,b,c)
  for _,q:=range []string{"SELECT hstore_to_jsonb_loose('a=>"+number+"'::hstore)","SELECT hstore_to_jsonb_loose($1)","SELECT hstore_to_jsonb_loose($1::hstore)"}{
   var args []any;if strings.Contains(q,"$1"){args=[]any{"a=>"+number}}
   want:=fixture.ControlsReferenceDiagnostic("22003","value overflows numeric format")
   for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,q,prepared,true,false,want,args...)}
   if len(args)>0{
    stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close();held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
    hstoreError(t,p,b,held,q,false,true,false,want,args...)
    hstoreRun(t,p,b,held,hstoreJSONBSelect(b,q,[]string{"hstore_to_jsonb_loose"},[][]any{{`{"a": 1}`}},false,"a=>1"))
   }
  }
  for _,expression:=range []string{"hstore_to_jsonb($1::hstore)","($1::hstore)::jsonb"}{hstoreRun(t,p,b,c,hstoreJSONBSelect(b,"SELECT "+expression+" AS j",[]string{"j"},[][]any{{`{"a": "`+number+`"}`}},false,"a=>"+number))}
 })})}
 for _,number:=range []string{"0e999999999","-0e999999999","0e-999999999","-0e-999999999"}{t.Run("zero/"+number,func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  hstoreInstall(t,p,b,c)
  q:="SELECT hstore_to_jsonb_loose($1::hstore) AS j"
  for _,prepared:=range []bool{false,true}{
   if strings.Contains(number,"e-"){
    want:=fixture.ControlsReferenceDiagnostic("22003","value overflows numeric format")
    hstoreError(t,p,b,c,q,prepared,true,false,want,"a=>"+number)
   }else{
    s:=hstoreJSONBSelect(b,q,[]string{"j"},[][]any{{`{"a": 0}`}},false,"a=>"+number)
    if prepared{s=fixture.Prepared(s)}
    hstoreRun(t,p,b,c,s)
   }
  }
 })})}
 t.Run("evaluation_atomicity",func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){hstoreInstall(t,p,b,c);hstoreJSONBOverflowAtomicity(t,p,b,c)})})
}

func hstoreJSONBOverflowAtomicity(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 t.Helper()
 hstoreRun(t,p,b,c,fixture.Exec("CREATE TABLE hs_jsonb_overflow(id integer,j jsonb)",0))
 hstoreRun(t,p,b,c,fixture.Exec(`INSERT INTO hs_jsonb_overflow VALUES (1,'{"sentinel": true}')`,1))
 q:="INSERT INTO hs_jsonb_overflow VALUES (2,hstore_to_jsonb_loose($1::hstore)),(3,hstore_to_jsonb_loose($2::hstore))"
 stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close();held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 unchanged:=hstoreJSONBSelect(b,"SELECT j FROM hs_jsonb_overflow ORDER BY id",[]string{"j"},[][]any{{`{"sentinel": true}`}},true)
 for _,prepared:=range []bool{false,true}{
  want:=fixture.ControlsReferenceDiagnostic("22003","value overflows numeric format")
  hstoreError(t,p,b,c,q,prepared,false,false,want,"a=>1","a=>1e131072")
  hstoreRun(t,p,b,c,unchanged)
 }
 result,err:=stmt.ExecContext(p.Ctx,"a=>1","a=>1e131072");if err==nil || result!=nil{t.Fatalf("held overflow result %v/%v",result,err)}
 want:=fixture.ControlsReferenceDiagnostic("22003","value overflows numeric format");fixture.AssertDiagnostic(t,err,&want)
 hstoreRun(t,p,b,c,unchanged)
 // Import must fail before either function evaluation; parameter two is malformed.
 hstoreError(t,p,b,held,q,false,false,false,hstoreParameterDiagnostic("42601",`syntax error in hstore, near "b" at position 2`,2),"a=>1e131072","a=b")
 hstoreRun(t,p,b,c,unchanged)
 hstoreRun(t,p,b,held,fixture.Exec(q,2,"a=>1","a=>2.345e+4"))
 hstoreRun(t,p,b,c,hstoreJSONBSelect(b,"SELECT j FROM hs_jsonb_overflow ORDER BY id",[]string{"j"},[][]any{{`{"sentinel": true}`},{`{"a": 1}`},{`{"a": 23450}`}},true))
}
