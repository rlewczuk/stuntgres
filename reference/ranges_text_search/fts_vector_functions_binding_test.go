package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)
func TestReferenceFTSVectorFunctionsBinding(t *testing.T){
 ftsQueryFunctionsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
  for _,tc:=range []struct{expression string;want int64}{
   {"length('abc')",3},{"length('abc'::text)",3},{"length('abc'::bytea)",3},{"length(B'101')",3},{"length('[(0,0),(3,4)]'::path)",5},{"length('[(0,0),(3,4)]'::lseg)",5},
  }{typ:="INT4";var want any=tc.want;if tc.want==5{typ="FLOAT8";want=float64(5)};ftsQueryFunctionsValue(t,p,b,tc.expression,typ,want)}
  ftsQueryFunctionsValue(t,p,b,"pg_catalog.setweight('cat:1B'::tsvector,'AB')","TSVECTOR","'cat':1A")
  ftsQueryFunctionsValue(t,p,b,`ts_filter($1::tsvector,$2::"char"[])`,"TSVECTOR","'cat':1B",[]byte("cat:1B rat:2C"),[]byte("{B}"))
  for _,prepared:=range []bool{false,true}{
   s:=fixture.Select(`SELECT length(NULL::tsvector) AS n,strip(NULL::tsvector) AS v,tsvector_to_array(NULL::tsvector) AS a WHERE FALSE`,[]string{"n","v","a"},[]string{"INT4","TSVECTOR","_TEXT"},nil)
   s.Prepared=prepared;fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
  }
  query:=`SELECT setweight($1::tsvector,$2::"char",$3::text[]) AS result`
  stmt,err:=b.DB.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer stmt.Close()
  held:=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:query}
  for _,tc:=range []struct{v,w string;s any;want any}{
   {"cat:1B rat:2C","A","{cat}","'cat':1A 'rat':2C"},
   {"cat:1B rat:2C","D","{rat}","'cat':1B 'rat':2"},
   {"cat:1B","A",nil,nil},
   {"cat:1B","B","{cat}","'cat':1B"},
  }{fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(query,[]string{"result"},[]string{"TSVECTOR"},[][]any{{tc.want}},tc.v,tc.w,tc.s))}
  for range 2{for _,queryMode:=range []bool{false,true}{s:=fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("22023",`unrecognized weight: "Z"`),"cat:1B","Z","{missing}");s.SelectRows=queryMode;fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}}
  fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(query,[]string{"result"},[]string{"TSVECTOR"},[][]any{{"'cat':1A"}},"cat:1B","A","{cat}"))
  ftsQueryFunctionsValue(t,p,b,"$1::tsvector","TSVECTOR",ftsVectorFunctionsCanonical,ftsVectorFunctionsInput)
 })
}

func TestReferenceFTSVectorFunctionsFeatureSmoke(t *testing.T){
 ftsQueryFunctionsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
  s:=fixture.Select(`SELECT length($1::tsvector) AS n,setweight($1::tsvector,'a') AS a,setweight($1::tsvector,'D','{cat,rat,missing,NULL}') AS d,strip($1::tsvector) AS s,ts_delete($1::tsvector,'fat') AS one,ts_delete($1::tsvector,'{fat,rat,NULL}'::text[]) AS many,ts_filter($1::tsvector,'{a,b}') AS f,tsvector_to_array($1::tsvector) AS arr`,[]string{"n","a","d","s","one","many","f","arr"},[]string{"INT4","TSVECTOR","TSVECTOR","TSVECTOR","TSVECTOR","TSVECTOR","TSVECTOR","_TEXT"},[][]any{{int64(4),"'bare' 'cat':3A,7A 'fat':2A,4A 'rat':5A","'bare' 'cat':3,7 'fat':2,4 'rat':5","'bare' 'cat' 'fat' 'rat'","'bare' 'cat':3B,7C 'rat':5A","'bare' 'cat':3B,7C","'cat':3B 'rat':5A","{bare,cat,fat,rat}"}},ftsVectorFunctionsInput)
  fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s);fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Prepared(s))
  query:=`SELECT setweight($1::tsvector,$2::"char",$3::text[]) AS result`
  stmt,err:=b.DB.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer stmt.Close();held:=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:query}
  fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(query,[]string{"result"},[]string{"TSVECTOR"},[][]any{{"'a':1A"}},"a:1B","A","{a}"))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("22023",`unrecognized weight: "Z"`),"a:1B","Z","{a}"))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(query,[]string{"result"},[]string{"TSVECTOR"},[][]any{{"'b':2"}},"b:2A","D","{b}"))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(query,[]string{"result"},[]string{"TSVECTOR"},[][]any{{nil}},nil,"A","{a}"))
  ftsQueryFunctionsValue(t,p,b,"ts_filter($1::tsvector,'{D}')","TSVECTOR","'fat':2,4",ftsVectorFunctionsInput)
  ftsQueryFunctionsValue(t,p,b,"$1::tsvector","TSVECTOR",ftsVectorFunctionsCanonical,ftsVectorFunctionsInput)
 })
}
