package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func citextRegexpDiagnostic(code,message,name string) fixture.Diagnostic {
 d:=fixture.ControlsReferenceDiagnostic(code,message)
 // Independent SQL-wrapper expectation, refined only by the real release oracle.
 d.Context=`SQL function "`+name+`" statement 1`
 return d
}
func TestReferenceCitextRegexpPrepared(t *testing.T) {
 for _,d:=range citextRegexpDeclarations { t.Run(d.name+string(rune('0'+len(d.args))),func(t *testing.T){
 citextPatternScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 citextInstall(t,p,b,c)
 args:=[]any{"AbA","a"}; q:="SELECT public."+d.name+"($1::citext,$2::citext"
 if d.name=="regexp_replace" {q+=",$3::text";args=append(args,"X")}
 if len(args)<len(d.args) {q+=",$"+string(rune('1'+len(args)))+"::text";flag:="i";if d.name=="regexp_matches"||d.name=="regexp_replace" {flag="g"};args=append(args,flag)}
 q+=") AS v"
 if d.name=="regexp_replace"&&len(d.args)==4 {q="SELECT public.regexp_replace(flags=>$4::text,replacement=>$3::text,pattern=>$2::citext,string=>$1::citext) AS v"}
 stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
 held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 value:=any("{A}");if d.name=="regexp_replace" {value="XbA";if len(d.args)==4 {value="XbX"}};if d.name=="regexp_split_to_array" {value=`{"",b,""}`};if d.name=="regexp_split_to_table" {value=""}
 rows:=[][]any{{value}};if d.name=="regexp_matches"&&len(d.args)==3 {rows=[][]any{{"{A}"},{"{A}"}}};if d.name=="regexp_split_to_table" {rows=[][]any{{""},{"b"},{""}}}
 good:=fixture.Select(q,[]string{"v"},[]string{d.result},rows,args...);good.Bag=true
 citextPatternRoutes(t,p,b,c,good)
 fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
 if len(d.args)==3&&d.name!="regexp_replace"||len(d.args)==4 {
 invalidFlags:=append([]any(nil),args...);invalidFlags[len(invalidFlags)-1]="z"
 failure:=fixture.ErrorDiagnostic(q,citextRegexpDiagnostic("22023",`invalid regular expression option: "z"`,d.name),invalidFlags...);failure.SelectRows=true;failure.ErrorStage="execute"
 fixture.RunStep(t,p.Ctx,held,b.Recorder,failure)
 fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
 }
 badArgs:=append([]any(nil),args...);badArgs[1]="["
 bad:=fixture.ErrorDiagnostic(q,citextRegexpDiagnostic("2201B","invalid regular expression: brackets [] not balanced",d.name),badArgs...);bad.SelectRows=true;bad.ErrorStage="execute"
 fixture.RunStep(t,p.Ctx,held,b.Recorder,bad)
 nullArgs:=append([]any(nil),args...);nullArgs[1]=nil
 nullRows:=[][]any{{nil}};if d.set {nullRows=nil}
 fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,[]string{"v"},[]string{d.result},nullRows,nullArgs...))
 fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
 if d.name=="regexp_match"&&len(d.args)==3 {
 sensitive:=append([]any(nil),args...);sensitive[2]="c"
 fixture.RunStep(t,p.Ctx,held,b.Recorder,citextRegexpValue(q,"_TEXT",nil,sensitive...))
 fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
 }
 })}) }
}
func TestReferenceCitextRegexpDiagnostics(t *testing.T) {
 citextPatternScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 citextInstall(t,p,b,c)
 for _,tc:=range []struct{q,name,code,message,hint string;position int}{
 {"SELECT public.regexp_match('a'::citext,'a','z')","regexp_match","22023",`invalid regular expression option: "z"`,"",0},
 {"SELECT public.regexp_match('a'::citext,'[')","regexp_match","2201B","invalid regular expression: brackets [] not balanced","",0},
 {"SELECT public.regexp_match('a'::citext,'a','g')","regexp_match","22023",`regexp_match() does not support the "global" option`,"Use the regexp_matches function instead.",0},
 {"SELECT public.regexp_split_to_array('a'::citext,'a','g')","regexp_split_to_array","22023",`regexp_split_to_array() does not support the "global" option`,"",0},
 {"SELECT public.regexp_split_to_table('a'::citext,'a','g')","regexp_split_to_table","22023",`regexp_split_to_table() does not support the "global" option`,"",0},
 {"SELECT regexp_match()","","42883","function regexp_match() does not exist","No function matches the given name and argument types. You might need to add explicit type casts.",8},
 {"SELECT /*é*/ regexp_match()","","42883","function regexp_match() does not exist","No function matches the given name and argument types. You might need to add explicit type casts.",14},
 {"SELECT public.regexp_match('a'::citext,1)","","42883","function public.regexp_match(citext, integer) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.",8},
 {"SELECT 1 WHERE regexp_matches('AbA'::citext,'a') IS NOT NULL","","0A000","set-returning functions are not allowed in WHERE","",16},
 } {
 d:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message);if tc.name!="" {d=citextRegexpDiagnostic(tc.code,tc.message,tc.name)};d.Hint=tc.hint;d.Position=fixture.Position(tc.position)
 for _,prepared:=range []bool{false,true} {
 citextError(t,p,b,c,tc.q,prepared,true,tc.code=="42883"||tc.code=="0A000",d)
 }
 }
 explicit:=`SELECT public.regexp_match('ABC'::citext COLLATE "C",'abc'::citext COLLATE "POSIX")`
 conflict:=fixture.ControlsReferenceDiagnostic("42P21",`collation mismatch between explicit collations "C" and "POSIX"`)
 conflict.Position=citextPosition(explicit,`COLLATE "POSIX"`)
 s:=fixture.ErrorDiagnostic(explicit,conflict);s.SelectRows=true;citextPatternRoutes(t,p,b,c,s)
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`CREATE TABLE cr_collation(id integer,s citext COLLATE "C",p citext COLLATE "POSIX")`,0))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO cr_collation VALUES(1,'ABC','abc'),(2,NULL,'[')",2))
 demand:=fixture.ControlsReferenceDiagnostic("42P22","could not determine which collation to use for regular expression");demand.Hint="Use the COLLATE clause to set the collation explicitly."
 citextPatternRoutes(t,p,b,c,citextRegexpValue("SELECT regexp_match(s,p) AS v FROM cr_collation WHERE id=1","_TEXT","{ABC}"))
 s=fixture.ErrorDiagnostic("SELECT pg_catalog.regexp_match(s::text,p::text,'i') AS v FROM cr_collation WHERE id=1",demand);s.SelectRows=true;citextPatternRoutes(t,p,b,c,s)
 citextPatternRoutes(t,p,b,c,citextRegexpValue("SELECT regexp_match(s,p) AS v FROM cr_collation WHERE id=2","_TEXT",nil))
 for _,q:=range []string{
 "SELECT regexp_match(NULL::citext,'['::citext,'z'::text) AS v",
 "SELECT regexp_match('a'::citext,NULL::citext,'z'::text) AS v",
 "SELECT regexp_match('a'::citext,'['::citext,NULL::text) AS v",
 } {citextPatternRoutes(t,p,b,c,citextRegexpValue(q,"_TEXT",nil))}
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE cr_atomic(s citext,p citext,CHECK(regexp_match(s,p) IS NOT NULL))",0))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO cr_atomic VALUES('A','a')",1))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.ErrorDiagnostic("INSERT INTO cr_atomic VALUES('B','b'),('x','[')",fixture.ControlsReferenceDiagnostic("2201B","invalid regular expression: brackets [] not balanced")))
 citextPatternRoutes(t,p,b,c,fixture.Select("SELECT s::text AS s,p::text AS p FROM cr_atomic",[]string{"s","p"},[]string{"TEXT","TEXT"},[][]any{{"A","a"}}))
 })
}
