package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceCitextStringDiagnostics(t *testing.T) {
 cases:=[]struct{q,code,message,hint,token string;prepare bool}{
  {"SELECT strpos()","42883","function strpos() does not exist","No function matches the given name and argument types. You might need to add explicit type casts.","strpos",true},
  {"SELECT /*é*/ strpos()","42883","function strpos() does not exist","No function matches the given name and argument types. You might need to add explicit type casts.","strpos",true},
  {"SELECT public.strpos('a'::citext,1)","42883","function public.strpos(citext, integer) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.","public.strpos",true},
  {"SELECT public.split_part('a'::citext,','::citext,1::bigint)","42883","function public.split_part(citext, citext, bigint) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.","public.split_part",true},
  {"SELECT public.split_part('a'::citext,','::citext,1.0)","42883","function public.split_part(citext, citext, numeric) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.","public.split_part",true},
  {"SELECT public.replace('a'::citext,'a','X'::text)","42883","function public.replace(citext, unknown, text) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.","public.replace",true},
  {"SELECT public.translate('a'::text,'a'::citext,'X')","42883","function public.translate(text, citext, unknown) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.","public.translate",true},
  {"SELECT public.strpos(source=>'a'::citext,needle=>'a'::citext)","42883","function public.strpos(source => citext, needle => citext) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.","public.strpos",true},
  {"SELECT public.strpos(source=>'a'::citext,'a'::citext)","42601","positional argument cannot follow named argument","","'a'::citext)",true},
  {"SELECT public.strpos(source=>'a'::citext,source=>'a'::citext)","42601",`argument name "source" used more than once`,"","source=>'a'::citext)",true},
  {"SELECT public.replace('a'::citext,'a')","42883","function public.replace(citext, unknown) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.","public.replace",true},
  {"SELECT public.translate('a'::citext,'a','x','z')","42883","function public.translate(citext, unknown, unknown, unknown) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.","public.translate",true},
  {"SELECT pg_catalog.split_part('a',',',0)","22023","field position must not be zero","","",false},
  {"SELECT public.split_part('a'::citext,',','x')","22P02",`invalid input syntax for type integer: "x"`,"","'x'",true},
  {"SELECT public.split_part(NULL::citext,',','x')","22P02",`invalid input syntax for type integer: "x"`,"","'x'",true},
  {"SELECT public.split_part('a'::citext,',',2147483648::integer)","22003","integer out of range","","",false},
  {"SELECT public.strpos('a'::citext,'a') FILTER (WHERE true)","42809","FILTER specified, but public.strpos is not an aggregate function","","public.strpos",true},
  {"SELECT public.strpos('a'::citext,'a') OVER ()","42809","OVER specified, but public.strpos is not a window function nor an aggregate function","","public.strpos",true},
  {"SELECT public.strpos('a'::citext,'a'); nonsense","42601",`syntax error at or near "nonsense"`,"","nonsense",true},
 }
 for _,tc:=range cases {t.Run(tc.q,func(t *testing.T){
  citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   citextInstall(t,p,b,c)
   d:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message);d.Hint=tc.hint
   if tc.token!="" {d.Position=citextPosition(tc.q,tc.token)}
   for _,prepared:=range []bool{false,true} {citextError(t,p,b,c,tc.q,prepared,true,tc.prepare,d)}
  })
 })}
 t.Run("collation",func(t *testing.T){citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  citextInstall(t,p,b,c)
  for _,d:=range citextStringDeclarations {
   args:=`'ABC'::citext COLLATE "C",'a'::citext COLLATE "POSIX"`
   if d.name=="replace" {args+=",'X'::citext"};if d.name=="translate" {args+=",'X'::text"};if d.name=="split_part" {args+=",1"}
   q:="SELECT /*é*/ public."+d.name+"("+args+") AS v"
   want:=fixture.ControlsReferenceDiagnostic("42P21",`collation mismatch between explicit collations "C" and "POSIX"`);want.Position=citextPosition(q,`COLLATE "POSIX"`)
   for _,prepared:=range []bool{false,true} {citextError(t,p,b,c,q,prepared,true,true,want)}
  }
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`CREATE TABLE cs_collation(s citext COLLATE "C",d citext COLLATE "POSIX")`,0))
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO cs_collation VALUES('ABC','a'),(NULL,'a')",2))
  for _,tc:=range []struct{expr,typ string;v any}{
   {"replace(s,d,'X')","TEXT","XBC"},{"split_part(s,d,2)","TEXT","BC"},
   {"strpos(s,d)","INT4",int64(1)},{"translate(s,d,'X')","TEXT","XBC"},
  } {
   citextStringRoutes(t,p,b,c,citextStringValue("SELECT "+tc.expr+" AS v FROM cs_collation WHERE s IS NOT NULL",tc.typ,tc.v))
   citextStringRoutes(t,p,b,c,citextStringValue("SELECT "+tc.expr+" AS v FROM cs_collation WHERE s IS NULL",tc.typ,nil))
  }
  for _,collation:=range []string{"C","POSIX"} {
   q:=`SELECT replace('AbA'::citext COLLATE "`+collation+`",'a','X') AS r,split_part('aTb'::citext COLLATE "`+collation+`",'t',2) AS s,strpos('ABC'::citext COLLATE "`+collation+`",'a') AS p,translate('ABC'::citext COLLATE "`+collation+`",'a','X') AS t`
   citextStringRoutes(t,p,b,c,fixture.Select(q,[]string{"r","s","p","t"},[]string{"TEXT","TEXT","INT4","TEXT"},[][]any{{"XbX","b",int64(1),"XBC"}}))
  }
 })})
}
