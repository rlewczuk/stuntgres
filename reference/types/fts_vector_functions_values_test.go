package reference_test

import (
 "testing"
 "strings"
 "stuntgres/reference/fixture"
)
const ftsVectorFunctionsInput = "cat:3B,7C fat:2,4 rat:5A bare"
const ftsVectorFunctionsCanonical = "'bare' 'cat':3B,7C 'fat':2,4 'rat':5A"

func TestReferenceFTSVectorFunctionsValues(t *testing.T) {
 ftsQueryFunctionsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
  fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE fts_vf_values(v tsvector)",0))
  fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO fts_vf_values VALUES($1)",1,ftsVectorFunctionsInput))
  for _,tc:=range []struct{expression,typ string;want any}{
   {"length($1::tsvector)","INT4",int64(4)},
   {"setweight($1::tsvector,'a')","TSVECTOR","'bare' 'cat':3A,7A 'fat':2A,4A 'rat':5A"},
   {"setweight($1::tsvector,'D',ARRAY['cat','rat','missing',NULL])","TSVECTOR","'bare' 'cat':3,7 'fat':2,4 'rat':5"},
   {"strip($1::tsvector)","TSVECTOR","'bare' 'cat' 'fat' 'rat'"},
   {"ts_delete($1::tsvector,'fat')","TSVECTOR","'bare' 'cat':3B,7C 'rat':5A"},
   {"ts_delete($1::tsvector,ARRAY['fat','rat','fat',NULL,'missing'])","TSVECTOR","'bare' 'cat':3B,7C"},
   {"ts_filter($1::tsvector,'{a,b}')","TSVECTOR","'cat':3B 'rat':5A"},
   {"ts_filter($1::tsvector,'{D}')","TSVECTOR","'fat':2,4"},
   {"tsvector_to_array($1::tsvector)","_TEXT","{bare,cat,fat,rat}"},
  }{t.Run(tc.expression,func(t *testing.T){
   ftsQueryFunctionsValue(t,p,b,tc.expression,tc.typ,tc.want,ftsVectorFunctionsInput)
   ftsQueryFunctionsValue(t,p,b,replaceVectorParameter(tc.expression),tc.typ,tc.want)
   s:=fixture.Select("SELECT "+strings.ReplaceAll(tc.expression,"$1::tsvector","v")+" AS result FROM fts_vf_values",[]string{"result"},[]string{tc.typ},[][]any{{tc.want}})
   fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
   fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Prepared(s))
  })}
  fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT length(v) AS n,strip(v) AS s,tsvector_to_array(v) AS a,v FROM fts_vf_values",[]string{"n","s","a","v"},[]string{"INT4","TSVECTOR","_TEXT","TSVECTOR"},[][]any{{int64(4),"'bare' 'cat' 'fat' 'rat'","{bare,cat,fat,rat}",ftsVectorFunctionsCanonical}}))
 })
}
func replaceVectorParameter(s string)string {
 // Only the first vector operand is replaced; SQL quoting remains shared.
 for i:=range len(s)-1 {if s[i:i+2]=="$1" {return s[:i]+ftsTypesQuote(ftsVectorFunctionsInput)+s[i+2:]}}
 return s
}
func TestReferenceFTSVectorFunctionsSelectors(t *testing.T){
 ftsQueryFunctionsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
  for _,tc:=range []struct{expression,want string}{
   {"ts_delete($1::tsvector,'{cat,rat}')",ftsVectorFunctionsCanonical},
   {"ts_delete($1::tsvector,'{cat,rat}'::text[])","'bare' 'fat':2,4"},
   {"ts_delete($1::tsvector,'[0:1][4:5]={{fat,NULL},{rat,missing}}'::text[])","'bare' 'cat':3B,7C"},
   {"ts_delete($1::tsvector,ARRAY['Cat','','missing',NULL])",ftsVectorFunctionsCanonical},
   {"setweight($1::tsvector,'A','{}'::text[])",ftsVectorFunctionsCanonical},
   {"ts_delete($1::tsvector,'{}'::text[])",ftsVectorFunctionsCanonical},
   {"ts_filter($1::tsvector,'[0:1][4:5]={{a,B},{a,b}}'::\"char\"[])","'cat':3B 'rat':5A"},
   {"ts_filter($1::tsvector,'{}')",""},
   {"ts_filter(strip($1::tsvector),'{A,B,C,D}')",""},
   {"setweight(strip($1::tsvector),'A')","'bare' 'cat' 'fat' 'rat'"},
  }{t.Run(tc.expression,func(t *testing.T){ftsQueryFunctionsValue(t,p,b,tc.expression,"TSVECTOR",tc.want,ftsVectorFunctionsInput)})}
  for _,tc:=range []struct{expression,typ string;want any}{
   {"length(''::tsvector)","INT4",int64(0)},
   {"tsvector_to_array(''::tsvector)","_TEXT","{}"},
   {"setweight(''::tsvector,'A')","TSVECTOR",""},
   {"setweight(''::tsvector,'A','{missing}')","TSVECTOR",""},
   {"strip(''::tsvector)","TSVECTOR",""},
   {"ts_delete(''::tsvector,'missing')","TSVECTOR",""},
   {"ts_delete(''::tsvector,'{missing}'::text[])","TSVECTOR",""},
   {"ts_filter(''::tsvector,'{A,B,C,D}')","TSVECTOR",""},
   {"strip(strip('cat:1A'::tsvector))","TSVECTOR","'cat'"},
   {"tsvector_to_array('é:1A cat:2 cat:3'::tsvector)","_TEXT","{cat,é}"},
   {"setweight('a:1B,16383C'::tsvector,'D')","TSVECTOR","'a':1,16383"},
   {"ts_filter('a:1B,16383C'::tsvector,'{C}')","TSVECTOR","'a':16383C"},
   {"ts_delete($1::tsvector,ARRAY['space word','{x}','Cat'])","TSVECTOR","'cat':4"},
   {"tsvector_to_array($1::tsvector)","_TEXT",`{Cat,cat,"space word","{x}"}`},
  }{args:=[]any{};if tc.expression=="tsvector_to_array($1::tsvector)"||tc.expression=="ts_delete($1::tsvector,ARRAY['space word','{x}','Cat'])"{args=[]any{"'space word':1 '{x}':2 Cat:3 cat:4"}};ftsQueryFunctionsValue(t,p,b,tc.expression,tc.typ,tc.want,args...)}
  ftsQueryFunctionsValue(t,p,b,"tsvector_to_array($1::tsvector)","_TEXT",`{a'b,"x\\y"}`,`'a''b':1 'x\\y':2`)
  ftsQueryFunctionsValue(t,p,b,"ts_delete($1::tsvector,ARRAY[$2::text])","TSVECTOR",`'x\\y':2`,`'a''b':1 'x\\y':2`,"a'b")
 })
}
func TestReferenceFTSVectorFunctionsCollatedSelectors(t *testing.T){
 ftsQueryFunctionsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
  const vector="'cat:1B rat:2C'::tsvector"
  for _,tc:=range []struct{selector string;args []any;deleted,weighted any}{
   {`ARRAY['cat',NULL,'cat','missing'] COLLATE "C"`,nil,"'rat':2C","'cat':1A 'rat':2C"},
   {`ARRAY['Cat','missing'] COLLATE "POSIX"`,nil,"'cat':1B 'rat':2C","'cat':1B 'rat':2C"},
   {`NULL::text[] COLLATE "C"`,nil,nil,nil},
   {`$1::text[] COLLATE "C"`,[]any{"{cat,NULL,cat,missing}"},"'rat':2C","'cat':1A 'rat':2C"},
   {`$1::text[] COLLATE "C"`,[]any{nil},nil,nil},
  }{
   ftsQueryFunctionsValue(t,p,b,"ts_delete("+vector+","+tc.selector+")","TSVECTOR",tc.deleted,tc.args...)
   ftsQueryFunctionsValue(t,p,b,"setweight("+vector+",'A',"+tc.selector+")","TSVECTOR",tc.weighted,tc.args...)
  }
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)}
  run(fixture.Exec(`CREATE TABLE fts_vf_collated(id integer,a text[] COLLATE "C")`,0))
  run(fixture.Exec(`INSERT INTO fts_vf_collated VALUES(1,'{cat,NULL,cat,missing}'),(2,NULL),(3,'{Cat,missing}')`,3))
  check:=fixture.Select("SELECT id,ts_delete("+vector+",a) AS d,setweight("+vector+",'A',a) AS w,a FROM fts_vf_collated ORDER BY id",[]string{"id","d","w","a"},[]string{"INT4","TSVECTOR","TSVECTOR","_TEXT"},[][]any{
   {int64(1),"'rat':2C","'cat':1A 'rat':2C","{cat,NULL,cat,missing}"},
   {int64(2),nil,nil,nil},
   {int64(3),"'cat':1B 'rat':2C","'cat':1B 'rat':2C","{Cat,missing}"},
  })
  run(check);run(fixture.Prepared(check))
 })
}
func TestReferenceFTSVectorFunctionsNulls(t *testing.T){
 ftsQueryFunctionsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
  for _,tc:=range []struct{expression,typ string}{
   {"length(NULL::tsvector)","INT4"},{"tsvector_to_array(NULL::tsvector)","_TEXT"},{"strip(NULL::tsvector)","TSVECTOR"},
   {"setweight(NULL::tsvector,'A')","TSVECTOR"},{"setweight('a'::tsvector,NULL)","TSVECTOR"},
   {"setweight(NULL::tsvector,'A','{}')","TSVECTOR"},{"setweight('a'::tsvector,NULL,'{}')","TSVECTOR"},{"setweight('a'::tsvector,'A',NULL)","TSVECTOR"},
   {"ts_delete(NULL::tsvector,'a')","TSVECTOR"},{"ts_delete('a'::tsvector,NULL::text)","TSVECTOR"},
   {"ts_delete(NULL::tsvector,'{}'::text[])","TSVECTOR"},{"ts_delete('a'::tsvector,NULL::text[])","TSVECTOR"},
   {"ts_filter(NULL::tsvector,'{A}')","TSVECTOR"},{"ts_filter('a'::tsvector,NULL)","TSVECTOR"},
  }{ftsQueryFunctionsValue(t,p,b,tc.expression,tc.typ,nil)}
 })
}
