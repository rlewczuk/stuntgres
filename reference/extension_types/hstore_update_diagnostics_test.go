package reference_test

import (
 "context"
 "database/sql"
 "errors"
 "strconv"
 "strings"
 "testing"
 "unicode/utf8"
 "stuntgres/reference/fixture"
)

func TestReferenceHstoreUpdateDiagnostics(t *testing.T) {
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreInstall(t,p,b,c)
 for _,tc:=range []struct{prefix,literal,suffix,code,message,detail string}{
 {"/*雪*/ SELECT slice(","'a=b'","::hstore,ARRAY['a'])","42601",`syntax error in hstore, near "b" at position 2`,""},
 {"/*雪*/ SELECT 'a=>1'::hstore - ","'b'","","42601","syntax error in hstore: unexpected end of string",""},
 {"/*雪*/ SELECT delete('a=>1'::hstore,","'not-array'","::text[])","22P02",`malformed array literal: "not-array"`,`Array value must start with "{" or dimension information.`},
 } {
 want:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message);want.Detail=tc.detail;want.Position=strconv.Itoa(utf8.RuneCountInString(tc.prefix)+1)
 for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,tc.prefix+tc.literal+tc.suffix,prepared,true,true,want)}
 }
 for _,tc:=range []struct{expression,token,message string;operator bool}{
 {`delete('a=>1'::hstore,1)`,"delete","function delete(hstore, integer) does not exist",false},
 {`slice('a=>1'::hstore,ARRAY[1])`,"slice","function slice(hstore, integer[]) does not exist",false},
 {`'a=>1'::hstore - 1`,"-","operator does not exist: hstore - integer",true},
 } {
 q:="/*雪*/ SELECT "+tc.expression;want:=fixture.ControlsReferenceDiagnostic("42883",tc.message)
 want.Position=strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q,tc.token)])+1)
 want.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
 if tc.operator{want.Hint="No operator matches the given name and argument types. You might need to add explicit type casts."}
 for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,q,prepared,true,true,want)}
 }
 for _,name:=range []string{"delete","slice"} {
 q:=`SELECT `+name+`($1::hstore,$2::text[]) AS v`
 stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
 held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 badStore:=hstoreParameterDiagnostic("42601",`syntax error in hstore, near "b" at position 2`,1)
 badArray:=hstoreParameterDiagnostic("22P02",`malformed array literal: "not-array"`,2);badArray.Detail=`Array value must start with "{" or dimension information.`
 for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,q,prepared,true,false,badStore,"a=b","{a}");hstoreError(t,p,b,c,q,prepared,true,false,badArray,"a=>1","not-array")}
 hstoreError(t,p,b,held,q,false,true,false,badStore,"a=b","{a}");hstoreError(t,p,b,held,q,false,true,false,badArray,"a=>1","not-array")
 want:=any("");if name=="slice"{want=`"a"=>"1"`}
 hstoreRun(t,p,b,held,hstoreSelect(b,q,[]string{"v"},[][]any{{want}},nil,"a=>1","{a}"))
 stmt.Close()
 }
 })
}

func TestReferenceHstoreUpdateAtomicity(t *testing.T) {
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreInstall(t,p,b,c)
 hstoreRun(t,p,b,c,fixture.Exec("CREATE TABLE hs_update_atomic(id integer,v hstore)",0))
 hstoreRun(t,p,b,c,fixture.Exec("INSERT INTO hs_update_atomic VALUES (1,'sentinel=>1')",1))
 q:=`INSERT INTO hs_update_atomic VALUES (2,slice('a=>1'::hstore,$1::text[])),(3,slice('a=>1'::hstore,$2::text[]))`
 want:=hstoreParameterDiagnostic("22P02",`malformed array literal: "not-array"`,2);want.Detail=`Array value must start with "{" or dimension information.`
 verify:=func(){hstoreRun(t,p,b,c,fixture.Select("SELECT id,v::text AS v FROM hs_update_atomic ORDER BY id",[]string{"id","v"},[]string{"INT4","TEXT"},[][]any{{int64(1),`"sentinel"=>"1"`}}))}
 for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,q,prepared,false,false,want,"{a}","not-array");verify()}
 stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
 held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 hstoreError(t,p,b,held,q,false,false,false,want,"{a}","not-array");verify()
 pairInsert:=`INSERT INTO hs_update_atomic VALUES (4,'a=>1'::hstore || $1::hstore),(5,delete('a=>1'::hstore,$2::hstore))`
 badPair:=hstoreParameterDiagnostic("42601",`syntax error in hstore, near "b" at position 2`,2)
 for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,pairInsert,prepared,false,false,badPair,"b=>2","a=b");verify()}
 pairStmt,err:=c.PrepareContext(p.Ctx,pairInsert);if err!=nil{t.Fatal(err)};defer pairStmt.Close()
 pairHeld:=fixture.HeldStatement{Executor:c,Statement:pairStmt,SQL:pairInsert}
 hstoreError(t,p,b,pairHeld,pairInsert,false,false,false,badPair,"b=>2","a=b");verify()
 hstoreRun(t,p,b,held,fixture.Exec(q,2,"{a}","{}"))
 hstoreRun(t,p,b,pairHeld,fixture.Exec(pairInsert,2,"b=>2","a=>1"))
 hstoreRun(t,p,b,c,hstoreSelect(b,"SELECT id,v FROM hs_update_atomic ORDER BY id",[]string{"id","v"},[][]any{{int64(1),`"sentinel"=>"1"`},{int64(2),`"a"=>"1"`},{int64(3),""},{int64(4),`"a"=>"1", "b"=>"2"`},{int64(5),""}},[]bool{true,true}))
 hstoreRun(t,p,b,c,fixture.Exec("CREATE TABLE hs_update_cast(id integer,raw text,v hstore)",0))
 hstoreRun(t,p,b,c,fixture.Exec("INSERT INTO hs_update_cast VALUES (1,'a=>1','old=>1'),(2,'a=b','old=>2')",2))
 bad:=fixture.ControlsReferenceDiagnostic("42601",`syntax error in hstore, near "b" at position 2`)
 update:=`UPDATE hs_update_cast SET v=slice(raw::hstore,ARRAY['a'])`
 for _,prepared:=range []bool{false,true}{
 hstoreError(t,p,b,c,update,prepared,false,false,bad)
 hstoreRun(t,p,b,c,fixture.Select("SELECT id,v::text AS v FROM hs_update_cast ORDER BY id",[]string{"id","v"},[]string{"INT4","TEXT"},[][]any{{int64(1),`"old"=>"1"`},{int64(2),`"old"=>"2"`}}))
 }
 if !b.PG {
 canceled,cancel:=context.WithCancel(p.Ctx);cancel()
 _,err:=c.ExecContext(canceled,`UPDATE hs_update_cast SET v=v || 'new=>1'::hstore`)
 if !errors.Is(err,context.Canceled){t.Fatal("ordinary cancellation",err)}
 hstoreRun(t,p,b,c,fixture.Select("SELECT id,v::text AS v FROM hs_update_cast ORDER BY id",[]string{"id","v"},[]string{"INT4","TEXT"},[][]any{{int64(1),`"old"=>"1"`},{int64(2),`"old"=>"2"`}}))
 }
 })
}
