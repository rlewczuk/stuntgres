package parser

import "testing"

func TestCitextStringCallSyntax(t *testing.T) {
 for _,tc:=range []struct{name,args string;count int}{
  {"strpos","'AbA'::citext,'a'",2},
  {"replace","'AbA'::citext,'a','X'",3},
  {"split_part","'aTb'::citext,'t',2",3},
  {"translate","'AbA'::citext,'a','X'",3},
 } {
  for _,prefix:=range []string{"","public."} {
   base:="SELECT /*é*/ "+prefix+tc.name+"("+tc.args+")"
   for _,window:=range []bool{false,true} {
    query:=base+" FILTER (WHERE true)";if window {query+=" OVER ()"}
    statements,err:=Parse(query);if err!=nil {t.Fatal("selected call envelope",query,err)}
    call,ok:=statements[0].(*Select).Targets[0].(FunctionExpr)
    if !ok||call.Name.Parts[len(call.Name.Parts)-1].Text!=tc.name||len(call.Args)!=tc.count||call.Filter==nil||(call.Window!=nil)!=window||call.Pos.Character!=14 {t.Fatal("lost selected call syntax",query,call)}
    if prefix!=""&&(len(call.Name.Parts)!=2||call.Name.Parts[0].Text!="public") {t.Fatal("lost qualified identity",call.Name)}
   }
   for _,suffix:=range []string{" FILTER (WHERE true) FILTER (WHERE false)"," OVER () FILTER (WHERE true)"," FILTER (WHERE)"," FILTER (WHERE true) nonsense junk"} {
    if _,err:=Parse(base+suffix);err==nil {t.Fatal("malformed selected continuation accepted",base+suffix)}
   }
  }
 }
}
