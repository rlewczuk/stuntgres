package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

type hstoreUpdateCase struct { expression, left, rightType string; right any; want any }

func hstoreUpdateValues(t *testing.T, cases []hstoreUpdateCase) {
 t.Helper()
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreInstall(t,p,b,c)
 for _,tc:=range cases {
 q:="SELECT "+tc.expression+" AS v"
 args:=[]any{tc.left,tc.right}
 s:=hstoreSelect(b,q,[]string{"v"},[][]any{{tc.want}},nil,args...)
 hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s))
 stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
 held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 hstoreRun(t,p,b,held,s)
 for _,args:=range [][]any{{nil,tc.right},{tc.left,nil}} {
 hstoreRun(t,p,b,held,hstoreSelect(b,q,[]string{"v"},[][]any{{nil}},nil,args...))
 }
 hstoreRun(t,p,b,held,s)
 if err:=stmt.Close();err!=nil{t.Fatal(err)}
 // The same selected signature is also exercised with literal operands.
 literalRight:="NULL";if text,ok:=tc.right.(string);ok{literalRight=hstoreLiteral(text)}
 literal:="SELECT "+replaceHstoreUpdateOperands(tc.expression,hstoreLiteral(tc.left),literalRight)+" AS v"
 hstoreRun(t,p,b,c,hstoreSelect(b,literal,[]string{"v"},[][]any{{tc.want}},nil))
 }
 })
}

func replaceHstoreUpdateOperands(expression,left,right string) string {
 // Parameter spellings are fixed in these independently declared cases.
 return strings.NewReplacer("$1",left,"$2",right).Replace(expression)
}

func TestReferenceHstoreUpdateValues(t *testing.T) {
 hstoreUpdateValues(t,[]hstoreUpdateCase{
 {`$1::hstore || $2::hstore`,"a=>1,b=>2","hstore","b=>3",`"a"=>"1", "b"=>"3"`},
 {`hs_concat($1::hstore,$2::hstore)`,"a=>1","hstore","a=>NULL",`"a"=>NULL`},
 {`$1::hstore || $2::hstore`,"a=>NULL","hstore","a=>2",`"a"=>"2"`},
 {`delete($1::hstore,$2::text)`,"a=>NULL,b=>2","text","a",`"b"=>"2"`},
 {`$1::hstore - $2::text`,"a=>1,b=>2","text","a",`"b"=>"2"`},
 {`delete($1::hstore,$2::text[])`,"a=>NULL,b=>2","text[]","{a,missing,NULL,a}",`"b"=>"2"`},
 {`$1::hstore - $2::text[]`,"a=>1,b=>2","text[]","{a}",`"b"=>"2"`},
 {`delete($1::hstore,$2::hstore)`,"a=>1,b=>2,c=>3","hstore","a=>4,b=>2",`"a"=>"1", "c"=>"3"`},
 {`$1::hstore - $2::hstore`,`a=>NULL,b=>"NULL"`,"hstore","a=>NULL,b=>NULL",`"b"=>"NULL"`},
 {`slice($1::hstore,$2::text[])`,"a=>NULL,b=>2","text[]","{a,missing,NULL,a}",`"a"=>NULL`},
 })
}

func TestReferenceHstoreUpdateBoundaries(t *testing.T) {
 hstoreUpdateValues(t,[]hstoreUpdateCase{
 {`$1::hstore || $2::hstore`,"","hstore","a=>NULL",`"a"=>NULL`},
 {`$1::hstore || $2::hstore`,"a=>NULL","hstore","",`"a"=>NULL`},
 {`delete($1::hstore,$2::text)`,`""=>x,a=>1`,"text","",`"a"=>"1"`},
 {`delete($1::hstore,$2::text)`,"a=>1","text","missing",`"a"=>"1"`},
 {`delete($1::hstore,$2::hstore)`,"a=>NULL","hstore",`a=>"NULL"`,`"a"=>NULL`},
 {`delete($1::hstore,$2::hstore)`,"a=>1","hstore","",`"a"=>"1"`},
 {`delete($1::hstore,$2::text[])`,"a=>NULL,b=>2","text[]","{}",`"a"=>NULL, "b"=>"2"`},
 {`delete($1::hstore,$2::text[])`,"a=>NULL,b=>2","text[]","{NULL,NULL}",`"a"=>NULL, "b"=>"2"`},
 {`delete($1::hstore,$2::text[])`,"a=>NULL,b=>2","text[]","[0:1][-1:1]={{b,a,a},{NULL,missing,b}}",""},
 {`slice($1::hstore,$2::text[])`,"a=>NULL,b=>2","text[]","[0:1][-1:1]={{b,a,a},{NULL,missing,b}}",`"a"=>NULL, "b"=>"2"`},
 {`slice($1::hstore,$2::text[])`,"a=>NULL","text[]","{}",""},
 {`slice($1::hstore,$2::text[])`,"a=>NULL","text[]","{NULL,NULL}",""},
 {`slice($1::hstore,$2::text[])`,"a=>NULL","text[]","{missing}",""},
 {`delete($1::hstore,$2::text)`,"","text","a",""},
 {`delete($1::hstore,$2::text[])`,"","text[]","{a}",""},
 {`delete($1::hstore,$2::hstore)`,"","hstore","a=>1",""},
 {`slice($1::hstore,$2::text[])`,"","text[]","{a}",""},
 {`delete($1::hstore,$2::text)`,`"NULL"=>1,""=>2`,"text","NULL",`""=>"2"`},
 {`delete($1::hstore,$2::text)`,`"雪"=>1,a=>2`,"text","雪",`"a"=>"2"`},
 {`delete($1::hstore,$2::text)`,`"a\"b\\c"=>1,z=>2`,"text",`a"b\c`,`"z"=>"2"`},
 })
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreInstall(t,p,b,c)
 for _,q:=range []string{`SELECT delete('a=>1,b=>2'::hstore,'b')::text AS v`,`SELECT ('a=>1,b=>2'::hstore - 'b=>2')::text AS v`,`SELECT slice('a=>1,b=>2'::hstore,'{a}')::text AS v`} {
 s:=fixture.Select(q,[]string{"v"},[]string{"TEXT"},[][]any{{`"a"=>"1"`}});hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s))
 }
 })
}

func TestReferenceHstoreUpdateMetadata(t *testing.T) {
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreInstall(t,p,b,c)
 for _,q:=range []string{`SELECT slice('a=>NULL'::hstore,ARRAY['a']) AS v`,`SELECT slice(v,ARRAY['a']) AS v FROM (SELECT 'a=>NULL'::hstore AS v) s WHERE false`} {
 rows:=[][]any{{`"a"=>NULL`}};if strings.Contains(q,"WHERE false"){rows=nil}
 s:=hstoreSelect(b,q,[]string{"v"},rows,nil);hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s))
 }
 hstoreRun(t,p,b,c,fixture.Exec(`CREATE TABLE hs_update_meta(v hstore NOT NULL DEFAULT slice('a=>NULL'::hstore,ARRAY['a']))`,0))
 hstoreRun(t,p,b,c,fixture.Exec(`INSERT INTO hs_update_meta DEFAULT VALUES`,1))
 hstoreRun(t,p,b,c,hstoreSelect(b,`SELECT v FROM hs_update_meta`,[]string{"v"},[][]any{{`"a"=>NULL`}},[]bool{false}))
 hstoreRun(t,p,b,c,hstoreSelect(b,`SELECT v FROM hs_update_meta WHERE false`,[]string{"v"},nil,[]bool{false}))
 })
}
