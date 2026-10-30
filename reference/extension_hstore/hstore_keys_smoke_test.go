package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceHstoreKeysFeatureSmoke(t *testing.T) {
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreKeysViewSeed(t,p,b,c);hstoreKeysViewVerify(t,p,b,c)
 q:=`SELECT $1::text::hstore ? $2::text AS present`;stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close();held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 for _,tc:=range []struct{h any;want any}{{"a=>NULL",true},{"",false},{nil,nil},{"a=>1",true}}{s:=fixture.Select(q,[]string{"present"},[]string{"BOOL"},[][]any{{tc.want}},tc.h,"a");hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s));hstoreRun(t,p,b,held,s)}
 hstoreRun(t,p,b,c,fixture.ArrayReferenceSelect(`SELECT akeys('a=>NULL'::hstore) AS k,avals('a=>NULL'::hstore) AS n,avals('a=>"NULL"'::hstore) AS t`,[]string{"k","n","t"},[]string{"_TEXT","_TEXT","_TEXT"},[]string{"_TEXT","_TEXT","_TEXT"},[][]any{{"{a}","{NULL}",`{"NULL"}`}},b.PG))
 qbad:=`SELECT 'a=>1'::hstore ?& ARRAY[1]`;want:=fixture.ControlsReferenceDiagnostic("42883","operator does not exist: hstore ?& integer[]");want.Position="23";want.Hint="No operator matches the given name and argument types. You might need to add explicit type casts."
 hstoreError(t,p,b,c,qbad,false,true,true,want)
 bad:=hstoreParameterDiagnostic("22P02",`malformed array literal: "not-array"`,2);bad.Detail=`Array value must start with "{" or dimension information.`
 hstoreError(t,p,b,c,`SELECT $1::hstore ?& $2::text[]`,false,true,false,bad,"a=>1","not-array")
 hstoreRun(t,p,b,c,fixture.Exec("CREATE TABLE hs_keys_smoke_atomic(id integer,v boolean)",0));hstoreRun(t,p,b,c,fixture.Exec("INSERT INTO hs_keys_smoke_atomic VALUES (1,true)",1))
 hstoreError(t,p,b,c,`INSERT INTO hs_keys_smoke_atomic VALUES (2,'a=>1'::hstore ?& $1::text[]),(3,'a=>1'::hstore ?& $2::text[])`,false,false,false,bad,"{a}","not-array")
 hstoreRun(t,p,b,c,fixture.Select("SELECT id,v FROM hs_keys_smoke_atomic ORDER BY id",[]string{"id","v"},[]string{"INT4","BOOL"},[][]any{{int64(1),true}}))
 })
 t.Run("controls",func(t *testing.T){testHstoreKeysControls(t,[]string{"same","fresh"})})
}
