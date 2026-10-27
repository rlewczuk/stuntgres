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

func hstoreUpdateSeed(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper();hstoreInstall(t,p,b,c)
 hstoreRun(t,p,b,c,fixture.Exec("CREATE TABLE hs_update_data(id integer,v hstore)",0))
 hstoreRun(t,p,b,c,fixture.Exec(`INSERT INTO hs_update_data VALUES (1,'a=>NULL,b=>2'),(2,''),(3,NULL),(4,hstore('a',$$x"y\z$$))`,4))
 hstoreRun(t,p,b,c,fixture.Exec(`CREATE VIEW hs_update_view AS SELECT id,hs_concat(v,'b=>3'::hstore)::text AS cat,delete(v,'a'::text)::text AS scalar,delete(v,ARRAY['a'])::text AS arr,delete(v,'a=>NULL,b=>9'::hstore)::text AS pair,slice(v,ARRAY['a','missing'])::text AS sliced,(v || 'b=>3'::hstore)::text AS op_cat,(v - 'a'::text)::text AS op_scalar,(v - ARRAY['a'])::text AS op_arr,(v - 'a=>NULL,b=>9'::hstore)::text AS op_pair FROM hs_update_data`,0))
 hstoreRun(t,p,b,c,fixture.Exec(`CREATE TABLE hs_update_defaults(id integer,c hstore DEFAULT hs_concat('a=>1'::hstore,'a=>NULL'::hstore),s hstore DEFAULT delete('a=>1,b=>2'::hstore,'a'::text),a hstore DEFAULT delete('a=>1,b=>2'::hstore,ARRAY['b']),p hstore DEFAULT delete('a=>NULL,b=>2'::hstore,'a=>NULL'::hstore),v hstore DEFAULT slice('a=>NULL,b=>2'::hstore,ARRAY['a','missing']))`,0))
 hstoreRun(t,p,b,c,fixture.Exec("INSERT INTO hs_update_defaults(id) VALUES (1)",1))
}

func hstoreUpdateVerify(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 names:=[]string{"id","cat","scalar","arr","pair","sliced","op_cat","op_scalar","op_arr","op_pair"}
 types:=[]string{"INT4","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT"}
 want:=[][]any{
 {int64(1),`"a"=>NULL, "b"=>"3"`,`"b"=>"2"`,`"b"=>"2"`,`"b"=>"2"`,`"a"=>NULL`,`"a"=>NULL, "b"=>"3"`,`"b"=>"2"`,`"b"=>"2"`,`"b"=>"2"`},
 {int64(2),`"b"=>"3"`,"","","","",`"b"=>"3"`,"","",""},
 {int64(3),nil,nil,nil,nil,nil,nil,nil,nil,nil},
 {int64(4),`"a"=>"x\"y\\z", "b"=>"3"`,"","",`"a"=>"x\"y\\z"`,`"a"=>"x\"y\\z"`,`"a"=>"x\"y\\z", "b"=>"3"`,"","",`"a"=>"x\"y\\z"`},
 }
 s:=fixture.Select("SELECT id,cat,scalar,arr,pair,sliced,op_cat,op_scalar,op_arr,op_pair FROM hs_update_view ORDER BY id",names,types,want)
 hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s))
 hstoreRun(t,p,b,c,fixture.Select("SELECT c::text AS c,s::text AS s,a::text AS a,p::text AS p,v::text AS v FROM hs_update_defaults WHERE id=1",[]string{"c","s","a","p","v"},[]string{"TEXT","TEXT","TEXT","TEXT","TEXT"},[][]any{{`"a"=>NULL`,`"b"=>"2"`,`"a"=>"1"`,`"b"=>"2"`,`"a"=>NULL`}}))
}

func TestReferenceHstoreUpdateConsumers(t *testing.T) {
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreUpdateSeed(t,p,b,c);hstoreUpdateVerify(t,p,b,c)
 hstoreRun(t,p,b,c,fixture.Exec(`UPDATE hs_update_data SET v=v || 'b=>3'::hstore WHERE id=1`,1))
 hstoreRun(t,p,b,c,fixture.Select("SELECT v::text AS v FROM hs_update_data WHERE id=1",[]string{"v"},[]string{"TEXT"},[][]any{{`"a"=>NULL, "b"=>"3"`}}))
 hstoreRun(t,p,b,c,fixture.Prepared(fixture.Exec(`UPDATE hs_update_data SET v=delete(v,'b=>3'::hstore) WHERE id=1`,1)))
 hstoreRun(t,p,b,c,fixture.Exec(`UPDATE hs_update_data SET v=slice(v,ARRAY['a','missing']) WHERE id=1`,1))
 hstoreRun(t,p,b,c,hstoreSelect(b,"SELECT id,v FROM hs_update_data ORDER BY id",[]string{"id","v"},[][]any{{int64(1),`"a"=>NULL`},{int64(2),""},{int64(3),nil},{int64(4),`"a"=>"x\"y\\z"`}},[]bool{true,true}))
 })
}

func TestReferenceHstoreUpdateControls(t *testing.T) {
 phases:=[]string{"same","fresh","expression"}
 for slot:=57;slot<=65;slot++ {phases=append(phases,fmt.Sprintf("signature_%d",slot),fmt.Sprintf("kind_%d",slot))}
 for _,phase:=range phases {t.Run(phase,func(t *testing.T){
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreUpdateSeed(t,p,b,c);hstoreUpdateVerify(t,p,b,c);hstoreUpdateCatalog(t,p,b,c)
 if b.PG{return}
 dump:=hstoreDump(t,p,p.Native)
 retained,err:=b.DB.QueryContext(p.Ctx,"SELECT v || ''::hstore AS v FROM hs_update_data ORDER BY id");if err!=nil{t.Fatal(err)};defer retained.Close()
 columns,err:=retained.ColumnTypes();if err!=nil{t.Fatal(err)};hstoreColumns(t,false,columns,[]string{"v"},nil)
 q:=`SELECT slice($1::text::hstore,$2::text[])::text AS v`;stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
 held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 hstoreRun(t,p,b,held,fixture.Select(q,[]string{"v"},[]string{"TEXT"},[][]any{{`"a"=>NULL`}},"a=>NULL,b=>2","{a,missing,NULL,a}"))
 rawQuery:=`SELECT slice($1::hstore,$2::text[]) AS v`
 rawStmt,err:=c.PrepareContext(p.Ctx,rawQuery);if err!=nil{t.Fatal(err)};defer rawStmt.Close()
 rawHeld:=fixture.HeldStatement{Executor:c,Statement:rawStmt,SQL:rawQuery}
 hstoreRun(t,p,b,rawHeld,hstoreSelect(b,rawQuery,[]string{"v"},[][]any{{`"a"=>NULL`}},nil,"a=>NULL","{a}"))
 switch phase {
 case "same":
 if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
 if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
 if after:=hstoreDump(t,p,p.Native);!bytes.Equal(after,dump){t.Fatal("same restore changed dump")}
 case "fresh":
 destination:=fixture.NewFixture(t,false,true)
 if _,err:=destination.Native.ExecContext(p.Ctx,"CREATE TABLE hs_update_displace(v integer); CREATE EXTENSION hstore");err!=nil{t.Fatal(err)}
 if err:=stuntgres.Restore(p.Ctx,destination.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
 target:=fixture.Backend{DB:destination.Memory,Recorder:destination.MemoryRecord}
 hstoreUpdateVerify(t,p,target,destination.Memory);hstoreUpdateCatalog(t,p,target,destination.Memory)
 hstoreRun(t,p,target,destination.Memory,fixture.Exec("INSERT INTO hs_update_defaults(id) VALUES (2)",1))
 hstoreRun(t,p,target,destination.Memory,hstoreSelect(target,"SELECT v FROM hs_update_defaults WHERE id=2",[]string{"v"},[][]any{{`"a"=>NULL`}},[]bool{true}))
 hstoreRun(t,p,target,destination.Memory,hstoreSelect(target,`SELECT v || 'b=>3'::hstore AS v FROM hs_update_data WHERE id=1`,[]string{"v"},[][]any{{`"a"=>NULL, "b"=>"3"`}},nil))
 hstoreRun(t,p,target,destination.Memory,fixture.Exec(`UPDATE hs_update_data SET v=v - 'b'::text WHERE id=1`,1))
 default:
 invalid:=""
 if phase=="expression" {invalid=string(dump)+"\nCREATE TABLE hs_update_reject(v hstore);\nINSERT INTO hs_update_reject VALUES (slice('a=>1'::hstore,'not-array'::text[]));\n"} else {invalid=hstoreInvalidOwnerDump(t,dump,phase)}
 err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(invalid))
 if err==nil{t.Fatal("invalid staged restore accepted")}
 if phase!="expression" {var state interface{SQLState() string};if !strings.Contains(err.Error(),"invalid extension owner metadata")||errors.As(err,&state){t.Fatal("ordinary owner failure",err)}}
 if after:=hstoreDump(t,p,p.Native);!bytes.Equal(after,dump){t.Fatal("failed restore changed incumbent")}
 }
 hstoreUpdateVerify(t,p,b,c);hstoreUpdateCatalog(t,p,b,c)
 for _,tc:=range []struct{h,k,w any}{{"a=>NULL,b=>2","{a}",`"a"=>NULL`},{"a=>1","{}",""},{"a=>1",nil,nil},{nil,"{a}",nil},{"a=>2","{a}",`"a"=>"2"`}} {
 hstoreRun(t,p,b,held,fixture.Select(q,[]string{"v"},[]string{"TEXT"},[][]any{{tc.w}},tc.h,tc.k))
 }
 hstoreRun(t,p,b,c,hstoreSelect(b,`SELECT slice(v,ARRAY['a']) AS v FROM hs_update_data ORDER BY id`,[]string{"v"},[][]any{{`"a"=>NULL`},{""},{nil},{`"a"=>"x\"y\\z"`}},nil))
 if phase=="same" {
 stale:=fixture.ErrorDiagnostic(rawQuery,fixture.ControlsReferenceDiagnostic("0A000","cached plan must not change parameter type"),"a=>NULL","{a}")
 stale.SelectRows,stale.ErrorStage=true,"execute";hstoreRun(t,p,b,rawHeld,stale)
 } else {
 hstoreRun(t,p,b,rawHeld,hstoreSelect(b,rawQuery,[]string{"v"},[][]any{{`"a"=>NULL`}},nil,"a=>NULL","{a}"))
 }
 var actual []any
 for retained.Next(){var v any;if err:=retained.Scan(&v);err!=nil{t.Fatal(err)};actual=append(actual,v)}
 if err:=retained.Err();err!=nil{t.Fatal(err)}
 if !reflect.DeepEqual(actual,[]any{`"a"=>NULL, "b"=>"2"`, "",nil,`"a"=>"x\"y\\z"`}){t.Fatal("detached rows changed",actual)}
 hstoreColumns(t,false,columns,[]string{"v"},nil)
 if phase=="same"&&p.Postgres!=nil {hstoreUpdateReplayDump(t,p,dump,false)}
 })
 })}
}

func hstoreUpdateReplayDump(t *testing.T,p *fixture.Pair,dump []byte,presentation bool) {
 t.Helper()
 if _,err:=p.Postgres.ExecContext(p.Ctx,"DROP SCHEMA public CASCADE; CREATE SCHEMA public");err!=nil{t.Fatal(err)}
 if _,err:=p.Postgres.ExecContext(p.Ctx,string(dump));err!=nil{t.Fatal(err)}
 pg:=fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord}
 hstoreUpdateVerify(t,p,pg,p.Postgres)
 hstoreRun(t,p,pg,p.Postgres,fixture.Exec("INSERT INTO hs_update_defaults(id) VALUES (2)",1))
 if presentation {hstoreUpdateCurrent(t,p,pg,p.Postgres)}
 cleanup:="DROP SCHEMA public CASCADE; CREATE SCHEMA public"
 if presentation {cleanup="DROP SCHEMA public CASCADE; DROP SCHEMA hs_update_replay CASCADE; CREATE SCHEMA public"}
 if _,err:=p.Postgres.ExecContext(p.Ctx,cleanup);err!=nil{t.Fatal(err)}
}

func TestReferenceHstoreUpdateSmoke(t *testing.T) {
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreUpdateSeed(t,p,b,c);hstoreUpdateVerify(t,p,b,c)
 hstoreRun(t,p,b,c,fixture.Exec("CREATE TABLE hs_update_smoke(id integer,v hstore)",0))
 hstoreRun(t,p,b,c,fixture.Exec("INSERT INTO hs_update_smoke VALUES (1,'a=>1,b=>2')",1))
 hstoreRun(t,p,b,c,fixture.Exec("UPDATE hs_update_smoke SET v=v || 'b=>3'::hstore WHERE id=1",1))
 hstoreRun(t,p,b,c,fixture.Select("SELECT v::text AS v FROM hs_update_smoke ORDER BY id",[]string{"v"},[]string{"TEXT"},[][]any{{`"a"=>"1", "b"=>"3"`}}))
 hstoreRun(t,p,b,c,fixture.Select("SELECT delete(v,'a=>9,b=>3'::hstore)::text AS v FROM hs_update_smoke ORDER BY id",[]string{"v"},[]string{"TEXT"},[][]any{{`"a"=>"1"`}}))
 hstoreRun(t,p,b,c,fixture.Select(`SELECT slice('a=>NULL,b=>2'::hstore,ARRAY['a','missing'])::text AS v`,[]string{"v"},[]string{"TEXT"},[][]any{{`"a"=>NULL`}}))
 q:=`SELECT slice($1::hstore,$2::text[]) AS v`;stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
 held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 for _,tc:=range []struct{k,w any}{{"{a,missing,NULL,a}",`"a"=>NULL`},{"{}",""},{nil,nil}}{hstoreRun(t,p,b,held,hstoreSelect(b,q,[]string{"v"},[][]any{{tc.w}},nil,"a=>NULL,b=>2",tc.k))}
 bridgeQuery:=`SELECT slice($1::text::hstore,$2::text[])::text AS v`
 bridgeStmt,err:=c.PrepareContext(p.Ctx,bridgeQuery);if err!=nil{t.Fatal(err)};defer bridgeStmt.Close()
 bridge:=fixture.HeldStatement{Executor:c,Statement:bridgeStmt,SQL:bridgeQuery}
 hstoreRun(t,p,b,bridge,fixture.Select(bridgeQuery,[]string{"v"},[]string{"TEXT"},[][]any{{`"a"=>NULL`}},"a=>NULL,b=>2","{a}"))
 if b.PG{return}
 dump:=hstoreDump(t,p,p.Native)
 if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
 if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
 hstoreUpdateVerify(t,p,b,c)
 hstoreRun(t,p,b,bridge,fixture.Select(bridgeQuery,[]string{"v"},[]string{"TEXT"},[][]any{{`"a"=>"2"`}},"a=>2,b=>3","{a}"))
 hstoreRun(t,p,b,c,hstoreSelect(b,`SELECT slice('a=>NULL'::hstore,ARRAY['a']) AS v`,[]string{"v"},[][]any{{`"a"=>NULL`}},nil))
 destination:=fixture.NewFixture(t,false,true)
 if err:=stuntgres.Restore(p.Ctx,destination.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
 target:=fixture.Backend{DB:destination.Memory,Recorder:destination.MemoryRecord};hstoreUpdateVerify(t,p,target,destination.Memory)
 if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(string(dump)+"\nCREATE TABLE hs_update_smoke_reject(v hstore);\nINSERT INTO hs_update_smoke_reject VALUES (slice('a=b'::hstore,ARRAY['a']));"));err==nil{t.Fatal("bad staged expression accepted")}
 if after:=hstoreDump(t,p,p.Native);!bytes.Equal(after,dump){t.Fatal("failed restore changed smoke state")}
 if p.Postgres!=nil{hstoreUpdateReplayDump(t,p,dump,false)}
 })
}
