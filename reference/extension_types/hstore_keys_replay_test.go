package reference_test

import (
 "bytes"
 "database/sql"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func hstoreKeysReplayVerify(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper();hstoreKeysViewVerify(t,p,b,c)
 hstoreRun(t,p,b,c,fixture.ArrayReferenceSelect(`SELECT hs_keys_replay.keys_now(v) AS k,hs_keys_replay.values_now(v) AS a FROM hs_keys WHERE id=4`,[]string{"k","a"},[]string{"_TEXT","_TEXT"},[]string{"_TEXT","_TEXT"},[][]any{{"{a}",`{"NULL"}`}},b.PG))
 hstoreRun(t,p,b,c,fixture.Select(`SELECT hs_keys_replay.exist_now(v,'a') AS e,hs_keys_replay.all_now(v,ARRAY['a']) AS a,hs_keys_replay.any_now(v,ARRAY['a']) AS y,hs_keys_replay.contains_now(v,'a=>NULL') AS c,hs_keys_replay.contained_now(v,'a=>NULL') AS d FROM hs_keys WHERE id=1`,[]string{"e","a","y","c","d"},[]string{"BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true,false}}))
 for _,tc:=range []struct{op,right,backer string}{{"?","text","exist_now(hstore,text)"},{"?&","text[]","all_now(hstore,text[])"},{"?|","text[]","any_now(hstore,text[])"},{"@>","hstore","contains_now(hstore,hstore)"},{"<@","hstore","contained_now(hstore,hstore)"}} {
 hstoreRun(t,p,b,c,fixture.Select(`SELECT oprcode=$2::regprocedure AS backing FROM pg_operator WHERE oid=$1::regoperator`,[]string{"backing"},[]string{"BOOL"},[][]any{{true}},tc.op+"(hstore,"+tc.right+")","hs_keys_replay."+tc.backer))
 }
}

func TestReferenceHstoreKeysReplay(t *testing.T) {
 fixture.SchemaReferenceCleanup(t,"hs_keys_replay")
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreKeysViewSeed(t,p,b,c);hstoreRun(t,p,b,c,fixture.Exec("CREATE SCHEMA hs_keys_replay",0))
 for _,tc:=range []struct{name,signature,renamed string}{{"exist","hstore,text","exist_now"},{"exists_all","hstore,text[]","all_now"},{"exists_any","hstore,text[]","any_now"},{"hs_contains","hstore,hstore","contains_now"},{"hs_contained","hstore,hstore","contained_now"},{"akeys","hstore","keys_now"},{"avals","hstore","values_now"}} {
 var before,after int64
 if err:=c.QueryRowContext(p.Ctx,"SELECT $1::regprocedure::oid",tc.name+"("+tc.signature+")").Scan(&before);err!=nil{t.Fatal(err)}
 hstoreRun(t,p,b,c,fixture.Exec("ALTER FUNCTION "+tc.name+"("+tc.signature+") RENAME TO "+tc.renamed,0));hstoreRun(t,p,b,c,fixture.Exec("ALTER FUNCTION "+tc.renamed+"("+tc.signature+") SET SCHEMA hs_keys_replay",0))
 if err:=c.QueryRowContext(p.Ctx,"SELECT $1::regprocedure::oid","hs_keys_replay."+tc.renamed+"("+tc.signature+")").Scan(&after);err!=nil{t.Fatal(err)}
 if before!=after{t.Fatal("presentation replaced provider identity",before,after)}
 }
 hstoreKeysReplayVerify(t,p,b,c)
 if !b.PG {
 dump:=hstoreDump(t,p,p.Native);destination:=fixture.NewFixture(t,false,true)
 if _,err:=destination.Native.ExecContext(p.Ctx,"CREATE TABLE hs_keys_replay_displace(v integer); CREATE EXTENSION hstore");err!=nil{t.Fatal(err)}
 if err:=stuntgres.Restore(p.Ctx,destination.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
 target:=fixture.Backend{DB:destination.Memory,Recorder:destination.MemoryRecord};hstoreKeysReplayVerify(t,p,target,destination.Memory)
 if after:=hstoreDump(t,p,destination.Native);!bytes.Equal(dump,after){t.Fatalf("renamed provider destination changed deterministic dump\nbefore=%s\nafter=%s",dump,after)}
 if p.Postgres!=nil {
 pg:=fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord}
 if _,err:=p.Postgres.ExecContext(p.Ctx,"DROP SCHEMA public CASCADE; CREATE SCHEMA public");err!=nil{t.Fatal(err)}
 if _,err:=p.Postgres.ExecContext(p.Ctx,string(dump));err!=nil{t.Fatal(err)};hstoreKeysReplayVerify(t,p,pg,p.Postgres)
 if _,err:=p.Postgres.ExecContext(p.Ctx,"DROP SCHEMA public CASCADE; DROP SCHEMA hs_keys_replay CASCADE; CREATE SCHEMA public");err!=nil{t.Fatal(err)}
 }
 }
 // Recreate after closing consumers; retired dynamic signatures are not promised.
 hstoreRun(t,p,b,c,fixture.Exec("DROP VIEW hs_keys_view",0));hstoreRun(t,p,b,c,fixture.Exec("DROP TABLE hs_keys",0));hstoreRun(t,p,b,c,fixture.Exec("DROP EXTENSION hstore",0))
 hstoreInstall(t,p,b,c)
 hstoreRun(t,p,b,c,fixture.Select(`SELECT 'a=>NULL'::hstore ? 'a' AS e,'a=>NULL'::hstore ?& ARRAY['a'] AS a,'a=>NULL'::hstore ?| ARRAY['a'] AS y,'a=>NULL'::hstore @> 'a=>NULL' AS c,'a=>NULL'::hstore <@ 'a=>NULL' AS d`,[]string{"e","a","y","c","d"},[]string{"BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true,true}}))
 hstoreRun(t,p,b,c,fixture.ArrayReferenceSelect(`SELECT akeys('a=>NULL'::hstore) AS k,avals('a=>NULL'::hstore) AS v`,[]string{"k","v"},[]string{"_TEXT","_TEXT"},[]string{"_TEXT","_TEXT"},[][]any{{"{a}","{NULL}"}},b.PG))
 })
}
