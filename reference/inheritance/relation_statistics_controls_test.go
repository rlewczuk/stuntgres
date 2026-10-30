package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "encoding/base64"
 "encoding/json"
 "errors"
 "math"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceRelationStatisticsNativeControls(t *testing.T){withRelationStats(t,func(s relationStatsScenario){
 s.exec("CREATE TABLE rst_t(a int)");s.run(fixture.Exec("INSERT INTO rst_t VALUES(7),(8)",2))
 s.restore("'relpages',-17,'reltuples',21.5::real,'relallvisible',-24,'relallfrozen',-27",true);s.stats(-17,21.5,-24,-27)
 s.run(fixture.Select("SELECT pg_restore_attribute_stats('schemaname','public','relname','rst_t','attname','a','inherited',false,'null_frac',.25::real)",[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.exec("CREATE TABLE rst_partial(a int)")
 s.run(fixture.Select(relationStatsRestore("rst_partial","'reltuples',22.5::real"),[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.exec("ALTER TABLE rst_partial RENAME TO rst_renamed")
 s.exec("CREATE TABLE rst_partitioned(a int) PARTITION BY RANGE(a)")
 s.run(fixture.Select(relationStatsRestore("rst_partitioned","'reltuples',23.5::real"),[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.exec("CREATE TABLE rst_cleared(a int)");s.run(fixture.Select("SELECT pg_clear_relation_stats('public','rst_cleared')",[]string{"pg_clear_relation_stats"},[]string{"VOID"},[][]any{{""}}))
 s.exec("CREATE VIEW rst_replay_restore AS SELECT pg_restore_relation_stats(VARIADIC kwargs => '{schemaname,public,relname,rst_t}'::text[]) AS v")
 s.exec("CREATE VIEW rst_replay_clear AS SELECT pg_clear_relation_stats(relname=>'rst_cleared',schemaname=>'public') IS NULL AS v")
 s.run(fixture.Select("SELECT null_frac FROM pg_stats WHERE schemaname='public' AND tablename='rst_t' AND attname='a' AND NOT inherited",[]string{"null_frac"},[]string{"FLOAT4"},[][]any{{float64(.25)}}))
 if s.b.PG{return}
 var saved bytes.Buffer;if err:=stuntgres.Dump(s.p.Ctx,s.p.Native,&saved);err!=nil{s.t.Fatal(err)}
 driver:=stuntgres.NewDriver();connector,err:=driver.OpenConnector(s.t.Name()+"/independent");if err!=nil{s.t.Fatal(err)};other:=sql.OpenDB(connector);defer other.Close()
 observed,recorder:=fixture.RecordedNativePool(s.t,connector)
 if _,err=other.ExecContext(s.p.Ctx,"CREATE TABLE sentinel(v int);INSERT INTO sentinel VALUES(7);SELECT pg_restore_relation_stats('schemaname','public','relname','sentinel','reltuples',71.5::real)");err!=nil{s.t.Fatal(err)}
 unchanged:=func(){s.t.Helper();var v int;var tuples float64;if err=other.QueryRowContext(s.p.Ctx,"SELECT v FROM sentinel").Scan(&v);err!=nil||v!=7{s.t.Fatal(v,err)};if err=other.QueryRowContext(s.p.Ctx,"SELECT reltuples FROM pg_class WHERE oid='sentinel'::regclass").Scan(&tuples);err!=nil||tuples!=71.5{s.t.Fatal("failed replacement changed imported tuples",tuples,err)}}
 bad:=relationStatsMalformedSnapshots(s,saved.String())
 for _,suffix:=range []string{"-- stuntgres-relation-statistics-v2 e30=\n","-- stuntgres-relation-statistics-v1 !!!\n","INVALID SQL"}{bad=append(bad,saved.String()+"\n"+suffix)}
 for i,script:=range bad{if err=stuntgres.Restore(s.p.Ctx,other,strings.NewReader(script));err==nil{s.t.Fatal("malformed import accepted",i)};unchanged()}
 canceled,cancel:=context.WithCancel(s.p.Ctx);cancel();if err=stuntgres.Restore(canceled,other,bytes.NewReader(saved.Bytes()));!errors.Is(err,context.Canceled){s.t.Fatal(err)};unchanged()
 if err=stuntgres.Restore(s.p.Ctx,other,bytes.NewReader(saved.Bytes()));err!=nil{s.t.Fatal(err)}
 fixture.RunStep(s.t,s.p.Ctx,observed,recorder,fixture.Select(relationStatsRead("rst_t"),[]string{"relpages","reltuples","relallvisible","relallfrozen"},[]string{"INT4","FLOAT4","INT4","INT4"},[][]any{{int64(-17),float64(21.5),int64(-24),int64(-27)}}))
 fixture.RunStep(s.t,s.p.Ctx,observed,recorder,fixture.Select("SELECT a FROM rst_t ORDER BY a",[]string{"a"},[]string{"INT4"},[][]any{{int64(7)},{int64(8)}}))
 fixture.RunStep(s.t,s.p.Ctx,observed,recorder,fixture.Select("SELECT null_frac FROM pg_stats WHERE schemaname='public' AND tablename='rst_t' AND attname='a' AND NOT inherited",[]string{"null_frac"},[]string{"FLOAT4"},[][]any{{float64(.25)}}))
 fixture.RunStep(s.t,s.p.Ctx,observed,recorder,fixture.Select(relationStatsRead("rst_renamed"),[]string{"relpages","reltuples","relallvisible","relallfrozen"},[]string{"INT4","FLOAT4","INT4","INT4"},[][]any{{int64(0),float64(22.5),int64(0),int64(0)}}))
 fixture.RunStep(s.t,s.p.Ctx,observed,recorder,fixture.Select(relationStatsRead("rst_partitioned"),[]string{"relpages","reltuples","relallvisible","relallfrozen"},[]string{"INT4","FLOAT4","INT4","INT4"},[][]any{{int64(0),float64(23.5),int64(0),int64(0)}}))
 fixture.RunStep(s.t,s.p.Ctx,observed,recorder,fixture.Select(relationStatsRead("rst_cleared"),[]string{"relpages","reltuples","relallvisible","relallfrozen"},[]string{"INT4","FLOAT4","INT4","INT4"},[][]any{{int64(0),float64(-1),int64(0),int64(0)}}))
 fixture.RunStep(s.t,s.p.Ctx,observed,recorder,fixture.Select("SELECT count(*) FROM pg_class WHERE relname='sentinel' AND relnamespace='public'::regnamespace",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
 var again bytes.Buffer;if err=stuntgres.Dump(s.p.Ctx,other,&again);err!=nil{s.t.Fatal(err)};if !bytes.Equal(saved.Bytes(),again.Bytes()){s.t.Fatal("exact logical redump changed")}
 fixture.RunStep(s.t,s.p.Ctx,observed,recorder,fixture.Select("SELECT v FROM rst_replay_restore",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
 fixture.RunStep(s.t,s.p.Ctx,observed,recorder,fixture.Select("SELECT v FROM rst_replay_clear",[]string{"v"},[]string{"BOOL"},[][]any{{false}}))
 relationStatsNativeLifetimes(s,saved.Bytes())
})}

func relationStatsMalformedSnapshots(s relationStatsScenario,script string)[]string{
 s.t.Helper();const prefix="-- stuntgres-relation-statistics-v1 "
 lines:=strings.Split(script,"\n");index:=-1;var original map[string]any
 for i,line:=range lines{if !strings.HasPrefix(line,prefix){continue};data,err:=base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(line,prefix));if err!=nil{s.t.Fatal(err)};var r map[string]any;if err=json.Unmarshal(data,&r);err!=nil{s.t.Fatal(err)};if r["Relation"]=="rst_t"{index=i;original=r;break}}
 if index<0{s.t.Fatal("missing genuine relation snapshot")}
 var scripts []string
 replace:=func(raw []byte){changed:=append([]string(nil),lines...);changed[index]=prefix+base64.StdEncoding.EncodeToString(raw);scripts=append(scripts,strings.Join(changed,"\n"))}
 for _,kind:=range []string{"unknown","mask-zero","mask-invalid","missing","unsupported","shared","nan","infinity","below"}{
  r:=make(map[string]any,len(original));for k,v:=range original{r[k]=v}
  switch kind{case "unknown":r["bogus"]=1;case "mask-zero":r["Mask"]=0;case "mask-invalid":r["Mask"]=16;case "missing":r["Relation"]="rst_missing";case "unsupported":r["Relation"]="rst_replay_restore";case "shared":r["Schema"],r["Relation"]="pg_catalog","pg_authid";case "nan":r["TupleBits"]=math.Float32bits(float32(math.NaN()));case "infinity":r["TupleBits"]=math.Float32bits(float32(math.Inf(1)));case "below":r["TupleBits"]=math.Float32bits(-2)}
  raw,err:=json.Marshal(r);if err!=nil{s.t.Fatal(err)};replace(raw)
 }
 replace([]byte(`{"Schema":"public","Schema":"public","Relation":"rst_t","Mask":1}`))
 replace([]byte(`{"Schema":`))
 scripts=append(scripts,script+"\n"+lines[index]+"\n")
 return scripts
}
