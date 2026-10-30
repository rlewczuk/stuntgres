package reference_test

import (
 "database/sql"
 "strconv"
 "strings"
 "testing"
 "unicode/utf8"
 "stuntgres/reference/fixture"
)

func TestReferenceHstoreKeysDiagnostics(t *testing.T) {
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreInstall(t,p,b,c)
 for _,tc:=range []struct{prefix,literal,suffix,code,message,detail string}{
 {"/*雪*/ SELECT akeys(","'a=b'","::hstore)","42601",`syntax error in hstore, near "b" at position 2`,""},
 {"/*雪*/ SELECT 'a=>1'::hstore ?& ","'not-array'","::text[]","22P02",`malformed array literal: "not-array"`,`Array value must start with "{" or dimension information.`},
 } {
 want:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message);want.Detail=tc.detail
 want.Position=strconv.Itoa(utf8.RuneCountInString(tc.prefix)+1)
 for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,tc.prefix+tc.literal+tc.suffix,prepared,true,true,want)}
 }
 for _,tc:=range []struct{expression,token,message string;operator bool}{
 {`akeys(1)`,"akeys","function akeys(integer) does not exist",false},
 {`avals()`,"avals","function avals() does not exist",false},
 {`'a=>1'::hstore ? 1`,"?","operator does not exist: hstore ? integer",true},
 {`'a=>1'::hstore ?& ARRAY[1]`,"?&","operator does not exist: hstore ?& integer[]",true},
 {`'a=>1'::hstore ?| ARRAY[1]`,"?|","operator does not exist: hstore ?| integer[]",true},
 {`'a=>1'::hstore @> 1`,"@>","operator does not exist: hstore @> integer",true},
 {`'a=>1'::hstore <@ 1`,"<@","operator does not exist: hstore <@ integer",true},
 {`'a'::text ? 'b'::text`,"?","operator does not exist: text ? text",true},
 {`1 @> 'a=>1'::hstore`,"@>","operator does not exist: integer @> hstore",true},
 } {
 q:="/*雪*/ SELECT "+tc.expression;want:=fixture.ControlsReferenceDiagnostic("42883",tc.message)
 want.Position=strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q,tc.token)])+1)
 want.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
 if tc.operator {want.Hint="No operator matches the given name and argument types. You might need to add explicit type casts."}
 for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,q,prepared,true,true,want)}
 }
 q:=`SELECT $1::hstore ?& $2::text[] AS v`;stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
 held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 badStore:=hstoreParameterDiagnostic("42601",`syntax error in hstore, near "b" at position 2`,1)
 badArray:=hstoreParameterDiagnostic("22P02",`malformed array literal: "not-array"`,2);badArray.Detail=`Array value must start with "{" or dimension information.`
 for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,q,prepared,true,false,badStore,"a=b","{a}");hstoreError(t,p,b,c,q,prepared,true,false,badArray,"a=>1","not-array")}
 hstoreError(t,p,b,held,q,false,true,false,badStore,"a=b","{a}");hstoreError(t,p,b,held,q,false,true,false,badArray,"a=>1","not-array")
 hstoreRun(t,p,b,held,fixture.Select(q,[]string{"v"},[]string{"BOOL"},[][]any{{true}},"a=>1","{a,NULL}"))
 hstoreRun(t,p,b,c,fixture.Exec("CREATE TABLE hs_keys_atomic(id integer,v boolean)",0));hstoreRun(t,p,b,c,fixture.Exec("INSERT INTO hs_keys_atomic VALUES (1,true)",1))
 insert:=`INSERT INTO hs_keys_atomic VALUES (2,'a=>1'::hstore ?& $1::text[]),(3,'a=>1'::hstore ?& $2::text[])`
 for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,insert,prepared,false,false,badArray,"{a}","not-array")}
 hstoreRun(t,p,b,c,fixture.Select("SELECT id,v FROM hs_keys_atomic ORDER BY id",[]string{"id","v"},[]string{"INT4","BOOL"},[][]any{{int64(1),true}}))
 })
}
