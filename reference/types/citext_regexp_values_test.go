package reference_test

import (
 "database/sql"
 "fmt"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

type citextRegexpDeclaration struct { name string; args []string; result string; set bool; rows float64 }
var citextRegexpDeclarations = []citextRegexpDeclaration{
 {"regexp_match", []string{"citext","citext"}, "_TEXT",false,0},
 {"regexp_match", []string{"citext","citext","text"}, "_TEXT",false,0},
 {"regexp_matches", []string{"citext","citext"}, "_TEXT",true,1},
 {"regexp_matches", []string{"citext","citext","text"}, "_TEXT",true,10},
 {"regexp_replace", []string{"citext","citext","text"}, "TEXT",false,0},
 {"regexp_replace", []string{"citext","citext","text","text"}, "TEXT",false,0},
 {"regexp_split_to_array", []string{"citext","citext"}, "_TEXT",false,0},
 {"regexp_split_to_array", []string{"citext","citext","text"}, "_TEXT",false,0},
 {"regexp_split_to_table", []string{"citext","citext"}, "TEXT",true,1000},
 {"regexp_split_to_table", []string{"citext","citext","text"}, "TEXT",true,1000},
}
func citextRegexpValue(q, typ string, value any, args ...any) fixture.Step {
 return fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{value}}, args...)
}
func citextRegexpCorpus(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 for _, s := range []fixture.Step{
 fixture.Exec("CREATE TABLE cr_source(id integer,s citext,p citext)",0),
 fixture.Exec("INSERT INTO cr_source VALUES (1,'AbA','a'),(2,'x','a'),(3,NULL,'a'),(4,'','')",4),
 } { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
}
func TestReferenceCitextRegexpValues(t *testing.T) {
 citextPatternScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 citextInstall(t,p,b,c)
 for _, tc := range []struct{ expression,typ string; value any }{
 {"regexp_match('AbA'::citext,'a')","_TEXT","{A}"},
 {"regexp_match('aBa'::citext,'(a)(b)')","_TEXT","{a,B}"},
 {"regexp_match('x'::citext,'a')","_TEXT",nil},
 {"regexp_match('b'::citext,'(a)?b')","_TEXT","{NULL}"},
 {"regexp_match('b'::citext,'(a*)b')","_TEXT",`{""}`},
 {"regexp_match(''::citext,'')","_TEXT",`{""}`},
 {"regexp_replace('AbA'::citext,'a','X')","TEXT","XbA"},
 {`regexp_replace('AbA'::citext,'(a)','<\1>','g')`,"TEXT","<A>b<A>"},
 {"regexp_split_to_array('helloTworld'::citext,'t')","_TEXT","{hello,world}"},
 {"regexp_split_to_array(''::citext,'')","_TEXT",`{""}`},
 {"regexp_split_to_array('é🙂x'::citext,'')","_TEXT","{é,🙂,x}"},
 {"regexp_split_to_array('a,,b,'::citext,',')","_TEXT",`{a,"",b,""}`},
 {"regexp_split_to_array('ab'::citext,'(x)?')","_TEXT","{a,b}"},
 } { citextPatternRoutes(t,p,b,c,citextRegexpValue("SELECT "+tc.expression+" AS v",tc.typ,tc.value)) }
 for _, d := range citextRegexpDeclarations { for null := range d.args {
 args := make([]string,len(d.args)); for i, typ := range d.args { value := "'a'"; if i==null { value="NULL" }; args[i]=value+"::"+typ }
 q := "SELECT public."+d.name+"("+strings.Join(args,",")+") AS v"
 rows:=[][]any{{nil}}; if d.set { rows=nil }
 citextPatternRoutes(t,p,b,c,fixture.Select(q,[]string{"v"},[]string{d.result},rows))
 } }
 citextRegexpCorpus(t,p,b,c)
 citextPatternRoutes(t,p,b,c,fixture.Select("SELECT id,regexp_match(s,p) AS v FROM cr_source ORDER BY id",[]string{"id","v"},[]string{"INT4","_TEXT"},[][]any{{int64(1),"{A}"},{int64(2),nil},{int64(3),nil},{int64(4),`{""}`}}))
 citextPatternRoutes(t,p,b,c,fixture.Select("SELECT id FROM cr_source WHERE regexp_match(s,p) IS NOT NULL ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(4)}}))
 citextPatternRoutes(t,p,b,c,fixture.Select("SELECT regexp_match(s,p) AS v FROM cr_source WHERE id=0",[]string{"v"},[]string{"_TEXT"},nil))
 })
}
func TestReferenceCitextRegexpFlags(t *testing.T) {
 citextPatternScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 citextInstall(t,p,b,c)
 for _, flag:=range []string{"","i","s","c","ic","ci"} { var want any="{A}"; if flag=="c"||flag=="ic" { want=nil }; citextPatternRoutes(t,p,b,c,citextRegexpValue(fmt.Sprintf("SELECT regexp_match('AbA'::citext,'a','%s') AS v",flag),"_TEXT",want)) }
 for _,tc:=range []struct{e,typ string;v any}{
 {"regexp_match('AbA'::citext,'(?c)a')","_TEXT",nil},
 {"regexp_match('AbA'::citext,'(?i)a','c')","_TEXT","{A}"},
 {"regexp_replace('AbA'::citext,'a','X','g')","TEXT","XbX"},
 {"regexp_replace('AbA'::citext,'a','X','c')","TEXT","AbA"},
 {"regexp_replace('AbA'::citext,'a','X','gci')","TEXT","XbX"},
 {"regexp_split_to_array('helloTworld'::citext,'t','c')","_TEXT","{helloTworld}"},
 {"regexp_split_to_array('helloTworld'::citext,'t','ci')","_TEXT","{hello,world}"},
 {"regexp_match(pattern=>'a',string=>'AbA'::citext)","_TEXT","{A}"},
 {"regexp_replace(flags=>'g',replacement=>'X',pattern=>'a',string=>'AbA'::citext)","TEXT","XbX"},
 {"pg_catalog.regexp_match('ABC'::citext,'abc')","_TEXT",nil},
 {"regexp_match('ABC'::citext,'abc'::text)","_TEXT",nil},
 {"public.regexp_match('ABC'::citext,'abc'::citext)","_TEXT","{ABC}"},
 {"regexp_replace('AbA'::citext,'a'::citext,'X',1,0,'i')","TEXT","XbX"},
 {"regexp_match('A.B'::citext,'a.b','q')","_TEXT","{A.B}"},
 {"regexp_match('AB'::citext,'a b','x')","_TEXT","{AB}"},
 {"regexp_match('AB'::citext,'a b','t')","_TEXT",nil},
 {"regexp_match('AB'::citext,'(ab)','e')","_TEXT",nil},
 {"regexp_match('(AB)'::citext,'(ab)','b')","_TEXT","{(AB)}"},
 {`regexp_match(E'a\nB'::citext,'^b','n')`,"_TEXT","{B}"},
 {`regexp_match(E'a\nB'::citext,'^b','m')`,"_TEXT","{B}"},
 {`regexp_match(E'a\nB'::citext,'^b','w')`,"_TEXT","{B}"},
 {`regexp_match(E'a\nB'::citext,'^b','p')`,"_TEXT",nil},
 {`regexp_match(E'a\nB'::citext,'^b','s')`,"_TEXT",nil},
 {`regexp_match('AB'::citext,'\(ab\)','b')`,"_TEXT","{AB}"},
 {`regexp_match('AB'::citext,'(?e)a+b')`,"_TEXT","{AB}"},
 {`regexp_match('ABC'::citext COLLATE "C",'abc'::citext COLLATE "C")`,"_TEXT","{ABC}"},
 {`regexp_match('ABC'::citext COLLATE "POSIX",'abc'::citext COLLATE "POSIX")`,"_TEXT","{ABC}"},
 } {t.Run(tc.e,func(t *testing.T){citextPatternRoutes(t,p,b,c,citextRegexpValue("SELECT "+tc.e+" AS v",tc.typ,tc.v))})}
 })
 t.Run("visibility",func(t *testing.T){
 fixture.SchemaReferenceCleanup(t,"cr_hidden")
 citextPatternScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,q:=range []string{"CREATE SCHEMA cr_hidden","CREATE EXTENSION citext SCHEMA cr_hidden","SET search_path TO pg_catalog,public"} {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
 citextPatternRoutes(t,p,b,c,citextRegexpValue("SELECT regexp_match('ABC'::cr_hidden.citext,'abc') AS v","_TEXT",nil))
 citextPatternRoutes(t,p,b,c,citextRegexpValue("SELECT cr_hidden.regexp_match('ABC'::cr_hidden.citext,'abc') AS v","_TEXT","{ABC}"))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("SET search_path TO cr_hidden,public,pg_catalog",0))
 citextPatternRoutes(t,p,b,c,citextRegexpValue("SELECT regexp_match('ABC'::citext,'abc') AS v","_TEXT","{ABC}"))
 })
 })
}
func TestReferenceCitextRegexpSets(t *testing.T) {
 citextPatternScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 citextInstall(t,p,b,c)
 for _,s:=range []fixture.Step{
 fixture.Select("SELECT v,ord FROM regexp_matches('AbA'::citext,'a','g') WITH ORDINALITY AS m(v,ord) ORDER BY ord",[]string{"v","ord"},[]string{"_TEXT","INT8"},[][]any{{"{A}",int64(1)},{"{A}",int64(2)}}),
 fixture.Select("SELECT v,ord FROM regexp_split_to_table('aTbtC'::citext,'t') WITH ORDINALITY AS m(v,ord) ORDER BY ord",[]string{"v","ord"},[]string{"TEXT","INT8"},[][]any{{"a",int64(1)},{"b",int64(2)},{"C",int64(3)}}),
 fixture.Select("SELECT regexp_matches('x'::citext,'a') AS v",[]string{"v"},[]string{"_TEXT"},nil),
 fixture.Select("SELECT regexp_split_to_table(''::citext,'') AS v",[]string{"v"},[]string{"TEXT"},[][]any{{""}}),
 fixture.Select("SELECT regexp_split_to_table('é🙂x'::citext,'') AS v",[]string{"v"},[]string{"TEXT"},[][]any{{"é"},{"🙂"},{"x"}}),
 fixture.Select("SELECT regexp_matches('AbA'::citext,'a','g') AS m,regexp_split_to_table('xTyTz'::citext,'t') AS s",[]string{"m","s"},[]string{"_TEXT","TEXT"},[][]any{{"{A}","x"},{"{A}","y"},{nil,"z"}}),
 fixture.Select("SELECT 7 AS sibling,regexp_matches('AbA'::citext,'a','g') AS v",[]string{"sibling","v"},[]string{"INT4","_TEXT"},[][]any{{int64(7),"{A}"},{int64(7),"{A}"}}),
 fixture.Select("SELECT regexp_matches(NULL::citext,'a') AS m,regexp_split_to_table('xTy'::citext,'t') AS s",[]string{"m","s"},[]string{"_TEXT","TEXT"},[][]any{{nil,"x"},{nil,"y"}}),
 fixture.Select("SELECT regexp_matches('x'::citext,'a') AS m,regexp_matches(NULL::citext,'a') AS n",[]string{"m","n"},[]string{"_TEXT","_TEXT"},nil),
 fixture.Select("SELECT regexp_split_to_table(s,'t') AS v FROM (SELECT NULL::citext AS s WHERE false) empty",[]string{"v"},[]string{"TEXT"},nil),
 fixture.Exec("CREATE TABLE cr_lateral(id integer,s citext)",0),
 fixture.Exec("INSERT INTO cr_lateral VALUES(1,'AtB'),(2,'x'),(3,NULL)",3),
 fixture.Select("SELECT id,v,ord FROM cr_lateral CROSS JOIN LATERAL regexp_split_to_table(s,'t') WITH ORDINALITY AS m(v,ord) ORDER BY id,ord",[]string{"id","v","ord"},[]string{"INT4","TEXT","INT8"},[][]any{{int64(1),"A",int64(1)},{int64(1),"B",int64(2)},{int64(2),"x",int64(1)}}),
 } {
 if s.SelectRows {s.Bag=!strings.Contains(s.Query,"ORDER BY");citextPatternRoutes(t,p,b,c,s)} else {fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 }
 })
}
