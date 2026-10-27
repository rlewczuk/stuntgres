package reference_test

import (
 "bytes"
 "database/sql"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func hstoreUpdateCurrent(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 q:=`SELECT hs_update_replay.concat_now('a=>1'::hstore,'a=>NULL'::hstore)::text AS c,hs_update_replay.scalar_now('a=>1,b=>2'::hstore,'a'::text)::text AS s,hs_update_replay.array_now('a=>1,b=>2'::hstore,ARRAY['b'])::text AS a,hs_update_replay.pair_now('a=>NULL,b=>2'::hstore,'a=>NULL'::hstore)::text AS p,hs_update_replay.slice_now('a=>NULL,b=>2'::hstore,ARRAY['a'])::text AS v`
 s:=fixture.Select(q,[]string{"c","s","a","p","v"},[]string{"TEXT","TEXT","TEXT","TEXT","TEXT"},[][]any{{`"a"=>NULL`,`"b"=>"2"`,`"a"=>"1"`,`"b"=>"2"`,`"a"=>NULL`}})
 hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s))
 hstoreUpdateCurrentOperators(t,p,b,c)
}

func hstoreUpdateCurrentOperators(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 for _,tc:=range []struct{expression,want string}{
 {`'a=>1'::hstore OPERATOR(hs_update_replay.||) 'a=>NULL'::hstore`,`"a"=>NULL`},
 {`'a=>1,b=>2'::hstore OPERATOR(hs_update_replay.-) 'a'::text`,`"b"=>"2"`},
 {`'a=>1,b=>2'::hstore OPERATOR(hs_update_replay.-) ARRAY['b']`,`"a"=>"1"`},
 {`'a=>NULL,b=>2'::hstore OPERATOR(hs_update_replay.-) 'a=>NULL'::hstore`,`"b"=>"2"`},
 } {
 s:=hstoreSelect(b,"SELECT "+tc.expression+" AS v",[]string{"v"},[][]any{{tc.want}},nil)
 hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s))
 }
}

func TestReferenceHstoreUpdatePresentationReplay(t *testing.T) {
 t.Run("operators_only",testHstoreUpdateOperatorOnlyReplay)
 fixture.SchemaReferenceCleanup(t,"hs_update_replay")
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreUpdateSeed(t,p,b,c)
 hstoreRun(t,p,b,c,fixture.Exec("CREATE SCHEMA hs_update_replay",0))
 for _,tc:=range []struct{name,signature,renamed string}{
 {"hs_concat","public.hstore,public.hstore","concat_now"},
 {"delete","public.hstore,text","scalar_now"},
 {"delete","public.hstore,text[]","array_now"},
 {"delete","public.hstore,public.hstore","pair_now"},
 {"slice","public.hstore,text[]","slice_now"},
 } {
 var before,after int64
 if err:=c.QueryRowContext(p.Ctx,"SELECT $1::regprocedure::oid","public."+tc.name+"("+tc.signature+")").Scan(&before);err!=nil{t.Fatal(err)}
 hstoreRun(t,p,b,c,fixture.Exec("ALTER FUNCTION public."+tc.name+"("+tc.signature+") RENAME TO "+tc.renamed,0))
 hstoreRun(t,p,b,c,fixture.Exec("ALTER FUNCTION public."+tc.renamed+"("+tc.signature+") SET SCHEMA hs_update_replay",0))
 if err:=c.QueryRowContext(p.Ctx,"SELECT $1::regprocedure::oid","hs_update_replay."+tc.renamed+"("+tc.signature+")").Scan(&after);err!=nil{t.Fatal(err)}
 if before!=after{t.Fatal("routine presentation replaced identity",tc.name,before,after)}
 }
 for _,tc:=range []struct{name,right string}{{"||","public.hstore"},{"-","text"},{"-","text[]"},{"-","public.hstore"}} {
 var before,after int64
 sig:="(public.hstore,"+tc.right+")"
 if err:=c.QueryRowContext(p.Ctx,"SELECT $1::regoperator::oid","public."+tc.name+sig).Scan(&before);err!=nil{t.Fatal(err)}
 hstoreRun(t,p,b,c,fixture.Exec("ALTER OPERATOR public."+tc.name+sig+" SET SCHEMA hs_update_replay",0))
 if err:=c.QueryRowContext(p.Ctx,"SELECT $1::regoperator::oid","hs_update_replay."+tc.name+sig).Scan(&after);err!=nil{t.Fatal(err)}
 if before!=after{t.Fatal("operator move replaced identity",tc.name,before,after)}
 }
 hstoreUpdateVerify(t,p,b,c);hstoreUpdateCurrent(t,p,b,c)
 hstoreRun(t,p,b,c,fixture.Exec("INSERT INTO hs_update_defaults(id) VALUES (3)",1))
 if b.PG{return}
 dump:=hstoreDump(t,p,p.Native)
 destination:=fixture.NewFixture(t,false,true)
 if _,err:=destination.Native.ExecContext(p.Ctx,"CREATE TABLE hs_update_displace(v integer); CREATE EXTENSION hstore");err!=nil{t.Fatal(err)}
 if err:=stuntgres.Restore(p.Ctx,destination.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
 if after:=hstoreDump(t,p,destination.Native);!bytes.Equal(after,dump){t.Fatal("presentation restore changed deterministic dump")}
 target:=fixture.Backend{DB:destination.Memory,Recorder:destination.MemoryRecord}
 hstoreUpdateVerify(t,p,target,destination.Memory);hstoreUpdateCurrent(t,p,target,destination.Memory)
 hstoreRun(t,p,target,destination.Memory,fixture.Exec("INSERT INTO hs_update_defaults(id) VALUES (4)",1))
 if p.Postgres!=nil{hstoreUpdateReplayDump(t,p,dump,true)}
 })
}

func testHstoreUpdateOperatorOnlyReplay(t *testing.T) {
 fixture.SchemaReferenceCleanup(t,"hs_update_replay")
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreUpdateSeed(t,p,b,c)
 hstoreRun(t,p,b,c,fixture.Exec("CREATE SCHEMA hs_update_replay",0))
 for _,tc:=range []struct{name,right string}{{"||","public.hstore"},{"-","text"},{"-","text[]"},{"-","public.hstore"}} {
 hstoreRun(t,p,b,c,fixture.Exec("ALTER OPERATOR public."+tc.name+"(public.hstore,"+tc.right+") SET SCHEMA hs_update_replay",0))
 }
 hstoreUpdateVerify(t,p,b,c);hstoreUpdateCurrentOperators(t,p,b,c)
 if b.PG{return}
 dump:=hstoreDump(t,p,p.Native)
 destination:=fixture.NewFixture(t,false,true)
 if _,err:=destination.Native.ExecContext(p.Ctx,"CREATE TABLE hs_update_operator_displace(v integer); CREATE EXTENSION hstore");err!=nil{t.Fatal(err)}
 if err:=stuntgres.Restore(p.Ctx,destination.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
 target:=fixture.Backend{DB:destination.Memory,Recorder:destination.MemoryRecord}
 hstoreUpdateVerify(t,p,target,destination.Memory);hstoreUpdateCurrentOperators(t,p,target,destination.Memory)
 if after:=hstoreDump(t,p,destination.Native);!bytes.Equal(after,dump){t.Fatal("operator-only restore changed deterministic dump")}
 hstoreRun(t,p,target,destination.Memory,fixture.Exec("INSERT INTO hs_update_defaults(id) VALUES (4)",1))
 if p.Postgres!=nil {
 if _,err:=p.Postgres.ExecContext(p.Ctx,"DROP SCHEMA public CASCADE; CREATE SCHEMA public");err!=nil{t.Fatal(err)}
 if _,err:=p.Postgres.ExecContext(p.Ctx,string(dump));err!=nil{t.Fatal(err)}
 pg:=fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord}
 hstoreUpdateVerify(t,p,pg,p.Postgres);hstoreUpdateCurrentOperators(t,p,pg,p.Postgres)
 hstoreRun(t,p,pg,p.Postgres,fixture.Exec("INSERT INTO hs_update_defaults(id) VALUES (4)",1))
 if _,err:=p.Postgres.ExecContext(p.Ctx,"DROP SCHEMA public CASCADE; DROP SCHEMA hs_update_replay CASCADE; CREATE SCHEMA public");err!=nil{t.Fatal(err)}
 }
 })
}
