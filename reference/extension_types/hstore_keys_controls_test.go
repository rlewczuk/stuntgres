package reference_test

import (
 "bytes"
 "database/sql"
 "errors"
 "fmt"
 "reflect"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func hstoreKeysViewSeed(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper();hstoreKeysSeed(t,p,b,c)
 hstoreRun(t,p,b,c,fixture.Exec(`CREATE VIEW hs_keys_view AS SELECT id,v ? 'a' AS one,v ?& ARRAY['a'] AS all_keys,v ?| ARRAY['a'] AS any_key,v @> 'a=>NULL' AS contains,v <@ 'a=>NULL' AS contained,exist(v,'a') AS named_one,exists_all(v,ARRAY['a']) AS named_all,exists_any(v,ARRAY['a']) AS named_any,hs_contains(v,'a=>NULL') AS named_contains,hs_contained(v,'a=>NULL') AS named_contained,akeys(v) AS keys,avals(v) AS vals FROM hs_keys WHERE v ? 'a'`,0))
}

func hstoreKeysViewVerify(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper();hstoreKeysVerify(t,p,b,c)
 q:=`SELECT id,one,all_keys,any_key,contains,contained,named_one,named_all,named_any,named_contains,named_contained FROM hs_keys_view ORDER BY id`
 hstoreRun(t,p,b,c,fixture.Select(q,[]string{"id","one","all_keys","any_key","contains","contained","named_one","named_all","named_any","named_contains","named_contained"},[]string{"INT4","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{int64(1),true,true,true,true,false,true,true,true,true,false},{int64(4),true,true,true,false,false,true,true,true,false,false}}))
 hstoreRun(t,p,b,c,fixture.ArrayReferenceSelect("SELECT keys,vals FROM hs_keys_view WHERE id=4",[]string{"keys","vals"},[]string{"_TEXT","_TEXT"},[]string{"_TEXT","_TEXT"},[][]any{{"{a}",`{"NULL"}`}},b.PG))
}

func TestReferenceHstoreKeysControls(t *testing.T) {
 phases:=[]string{"same","fresh","expression"};for slot:=40;slot<=50;slot++ {phases=append(phases,fmt.Sprintf("signature_%d",slot),fmt.Sprintf("kind_%d",slot))}
 testHstoreKeysControls(t,phases)
}

func testHstoreKeysControls(t *testing.T,phases []string) {
 t.Helper()
 for _,phase:=range phases {t.Run(phase,func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreKeysViewSeed(t,p,b,c);hstoreKeysViewVerify(t,p,b,c);hstoreKeysCatalog(t,p,b,c)
 if b.PG{return}
 dump:=hstoreDump(t,p,p.Native)
 retained,err:=b.DB.QueryContext(p.Ctx,"SELECT v ? 'a' AS present,akeys(v) AS k,avals(v) AS v FROM hs_keys WHERE id=4");if err!=nil{t.Fatal(err)};defer retained.Close()
 columns,err:=retained.ColumnTypes();if err!=nil{t.Fatal(err)};if len(columns)!=3||columns[0].DatabaseTypeName()!="BOOL"||columns[1].DatabaseTypeName()!="_TEXT"||columns[2].DatabaseTypeName()!="_TEXT"{t.Fatal("old predicate/array metadata")}
 q:=`SELECT $1::text::hstore ? $2::text AS v`;stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close();held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 hstoreRun(t,p,b,held,fixture.Select(q,[]string{"v"},[]string{"BOOL"},[][]any{{true}},"a=>NULL","a"))
 aq:=`SELECT akeys($1::text::hstore) AS k,avals($1::text::hstore) AS v`
 arrayStmt,err:=c.PrepareContext(p.Ctx,aq);if err!=nil{t.Fatal(err)};defer arrayStmt.Close()
 arrayHeld:=fixture.HeldStatement{Executor:c,Statement:arrayStmt,SQL:aq}
 hstoreRun(t,p,b,arrayHeld,fixture.ArrayReferenceSelect(aq,[]string{"k","v"},[]string{"_TEXT","_TEXT"},[]string{"_TEXT","_TEXT"},[][]any{{"{a}","{NULL}"}},b.PG,"a=>NULL"))
 switch phase {
 case "same":
 if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)};if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
 if after:=hstoreDump(t,p,p.Native);!bytes.Equal(dump,after){t.Fatalf("round trip changed deterministic dump\nbefore=%s\nafter=%s",dump,after)}
 case "fresh":
 destination:=fixture.NewFixture(t,false,true);if _,err:=destination.Native.ExecContext(p.Ctx,"CREATE TABLE hs_keys_displace(v integer); CREATE EXTENSION hstore");err!=nil{t.Fatal(err)}
 if err:=stuntgres.Restore(p.Ctx,destination.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
 target:=fixture.Backend{DB:destination.Memory,Recorder:destination.MemoryRecord};hstoreKeysViewVerify(t,p,target,destination.Memory);hstoreKeysCatalog(t,p,target,destination.Memory)
 if after:=hstoreDump(t,p,destination.Native);!bytes.Equal(dump,after){t.Fatalf("fresh destination changed deterministic dump\nbefore=%s\nafter=%s",dump,after)}
 default:
 var invalid string
 if phase=="expression" {invalid=string(dump)+"\nCREATE TABLE hs_keys_restore_atomic(v boolean);\nINSERT INTO hs_keys_restore_atomic VALUES ('a=>1'::hstore ?& 'not-array'::text[]);\n"} else {invalid=hstoreInvalidOwnerDump(t,dump,phase)}
 err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(invalid))
 if phase=="expression" {if err==nil{t.Fatal("malformed staged expression accepted")}} else {var state interface{SQLState() string};if err==nil||!strings.Contains(err.Error(),"invalid extension owner metadata")||errors.As(err,&state){t.Fatal("ordinary owner error",err)}}
 if after:=hstoreDump(t,p,p.Native);!bytes.Equal(after,dump){t.Fatalf("failed Restore changed incumbent dump\nbefore=%s\nafter=%s",dump,after)}
 }
 hstoreKeysViewVerify(t,p,b,c);hstoreKeysCatalog(t,p,b,c)
 for _,tc:=range []struct{h any;want any}{{"a=>NULL",true},{"",false},{nil,nil},{"a=>1",true}}{hstoreRun(t,p,b,held,fixture.Select(q,[]string{"v"},[]string{"BOOL"},[][]any{{tc.want}},tc.h,"a"))}
 for _,tc:=range []struct{h,k,v any}{{"a=>NULL","{a}","{NULL}"},{"","{}","{}"},{nil,nil,nil},{`a=>"NULL"`,"{a}",`{"NULL"}`}} {
 hstoreRun(t,p,b,arrayHeld,fixture.ArrayReferenceSelect(aq,[]string{"k","v"},[]string{"_TEXT","_TEXT"},[]string{"_TEXT","_TEXT"},[][]any{{tc.k,tc.v}},b.PG,tc.h))
 }
 var got [][]any;for retained.Next(){var present,k,v any;if err:=retained.Scan(&present,&k,&v);err!=nil{t.Fatal(err)};got=append(got,[]any{present,k,v})};if err:=retained.Err();err!=nil{t.Fatal(err)}
 if !reflect.DeepEqual(got,[][]any{{true,"{a}",`{"NULL"}`}})||columns[0].DatabaseTypeName()!="BOOL"||columns[1].DatabaseTypeName()!="_TEXT"{t.Fatal("detached rows or metadata changed",got)}
 if phase=="same"&&p.Postgres!=nil {
 pg:=fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord}
 if _,err:=p.Postgres.ExecContext(p.Ctx,"DROP SCHEMA public CASCADE; CREATE SCHEMA public");err!=nil{t.Fatal(err)}
 if _,err:=p.Postgres.ExecContext(p.Ctx,string(dump));err!=nil{t.Fatal(err)};hstoreKeysViewVerify(t,p,pg,p.Postgres);hstoreKeysCatalog(t,p,pg,p.Postgres)
 if _,err:=p.Postgres.ExecContext(p.Ctx,"DROP SCHEMA public CASCADE; CREATE SCHEMA public");err!=nil{t.Fatal(err)}
 }
 })})}
}
