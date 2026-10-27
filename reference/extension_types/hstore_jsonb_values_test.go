package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceHstoreJSONBValues(t *testing.T) {
 cases := []struct{input, strict, loose string}{
  {"", `{}`, `{}`}, {"a=>NULL", `{"a": null}`, `{"a": null}`}, {"a=>null", `{"a": null}`, `{"a": null}`}, {"a=>NuLl", `{"a": null}`, `{"a": null}`},
  {`a=>"NULL"`, `{"a": "NULL"}`, `{"a": "NULL"}`}, {"a=>012345", `{"a": "012345"}`, `{"a": "012345"}`},
  {"a=>t", `{"a": "t"}`, `{"a": true}`}, {"a=>f", `{"a": "f"}`, `{"a": false}`}, {`a=>"1"`, `{"a": "1"}`, `{"a": 1}`},
  {`""=>""`, `{"": ""}`, `{"": ""}`}, {`"é"=>"雪"`, `{"é": "雪"}`, `{"é": "雪"}`},
  {`a=>"<>&/'"`, `{"a": "<>&/'"}`, `{"a": "<>&/'"}`}, {`"a\"b"=>"c\\d"`, `{"a\"b": "c\\d"}`, `{"a\"b": "c\\d"}`},
  {"a=>\"line\n\t\x01\"", `{"a": "line\n\t\u0001"}`, `{"a": "line\n\t\u0001"}`},
  {`a=>"\\u0041"`, `{"a": "\\u0041"}`, `{"a": "\\u0041"}`},
  {"z=>1.00,a=>NULL,b=>t", `{"a": null, "b": "t", "z": "1.00"}`, `{"a": null, "b": true, "z": 1.00}`},
  {"雪=>3,aa=>2,z=>1,é=>4", `{"z": "1", "aa": "2", "é": "4", "雪": "3"}`, `{"z": 1, "aa": 2, "é": 4, "雪": 3}`},
 }
 for _, n := range []struct{input, output string}{{"0","0"},{"-0","0"},{"0.0","0.0"},{"-0.0","0.0"},{"12345","12345"},{"1.00","1.00"},{"1e0","1"},{"1E+02","100"},{"1e-02","0.01"},{"2.345e+4","23450"},{"1.230e-5","0.00001230"},{"92233720368547758081234567890","92233720368547758081234567890"}} {
  cases=append(cases,struct{input,strict,loose string}{"a=>"+n.input,`{"a": "`+n.input+`"}`,`{"a": `+n.output+`}`})
 }
 for _, v := range []string{"true","false","T","F","yes","on","+1","00","-01",".5","1.","1e","1e+","NaN","Infinity","-Infinity"," 1","1 ","[]","{}"} {
  want:=`{"a": "`+v+`"}`
  cases=append(cases,struct{input,strict,loose string}{`a=>"`+v+`"`,want,want})
 }
 for _, tc:=range cases { t.Run(tc.input,func(t *testing.T){
  hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   hstoreInstall(t,p,b,c)
   q:=hstoreJSONBQuery(hstoreLiteral(tc.input))
   s:=hstoreJSONBPair(b,q,[][]any{{tc.strict,tc.loose}})
   hstoreRun(t,p,b,c,s); hstoreRun(t,p,b,c,fixture.Prepared(s))
   hstoreRun(t,p,b,c,hstoreJSONBSelect(b,"SELECT ("+hstoreLiteral(tc.input)+"::hstore)::jsonb AS j",[]string{"j"},[][]any{{tc.strict}},false))
  })
 }) }
 t.Run("duplicates",func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  hstoreInstall(t,p,b,c)
  hstoreRun(t,p,b,c,hstoreJSONBPair(b,hstoreJSONBQuery("'a=>1,a=>1'"),[][]any{{`{"a": "1"}`,`{"a": 1}`}}))
  var strict,loose,cast string
  if err:=c.QueryRowContext(p.Ctx,"SELECT hstore_to_jsonb(v),hstore_to_jsonb_loose(v),v::jsonb FROM (SELECT 'a=>1,a=>2'::hstore AS v) s").Scan(&strict,&loose,&cast);err!=nil{t.Fatal(err)}
  if cast!=strict || !((strict==`{"a": "1"}` && loose==`{"a": 1}`)||(strict==`{"a": "2"}` && loose==`{"a": 2}`)){t.Fatalf("duplicate survivor %q/%q/%q",strict,loose,cast)}
 })})
}

func hstoreJSONBWhere(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 t.Helper()
 for _,tc:=range []struct{predicate string; ids []int64}{
  {`hstore_to_jsonb_loose(v)='{"a":1}'::jsonb`,[]int64{8}},
  {`hstore_to_jsonb(v)='{"a":"1"}'::jsonb`,[]int64{8}},
  {`v::jsonb='{"a":"1"}'::jsonb`,[]int64{8}},
  {`hstore_to_jsonb(v)='{"a":1}'::jsonb`,nil},
  {`hstore_to_jsonb_loose(v)='{"a":null}'::jsonb`,[]int64{4}},
  {`hstore_to_jsonb_loose(v) IS NULL`,[]int64{7}},
  {`hstore_to_jsonb_loose(v)='{}'::jsonb`,[]int64{6}},
 }{
  values:=[][]any{};for _,id:=range tc.ids{values=append(values,[]any{id})}
  s:=fixture.Select("SELECT id FROM hs_jsonb_controls WHERE "+tc.predicate+" ORDER BY id",[]string{"id"},[]string{"INT4"},values)
  hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s))
 }
}

func TestReferenceHstoreJSONBBindings(t *testing.T){
 for _,input:=range []string{"$1","$1::hstore"}{t.Run(input,func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  hstoreInstall(t,p,b,c)
  for _,name:=range []string{"hstore_to_jsonb","hstore_to_jsonb_loose"}{
   q:="SELECT public."+name+"("+input+") AS j";stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q};want:=`{"a": "1.00"}`;if name=="hstore_to_jsonb_loose"{want=`{"a": 1.00}`}
   for _,tc:=range []struct{arg,want any}{{"a=>1.00",want},{[]byte("a=>1.00"),want},{"",`{}`},{[]byte{},`{}`},{nil,nil},{[]byte(nil),nil}}{
    s:=hstoreJSONBSelect(b,q,[]string{"j"},[][]any{{tc.want}},false,tc.arg)
    hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s));hstoreRun(t,p,b,held,s)
   }
   hstoreError(t,p,b,held,q,false,true,false,hstoreParameterDiagnostic("42601",`syntax error in hstore, near "b" at position 2`,1),"a=b")
   hstoreRun(t,p,b,held,hstoreJSONBSelect(b,q,[]string{"j"},[][]any{{want}},false,"a=>1.00"))
  }
 })})}
 t.Run("projection_and_where",func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){hstoreJSONBControlSeed(t,p,b,c);hstoreJSONBControlVerify(t,p,b,c);hstoreJSONBWhere(t,p,b,c)})})
}
