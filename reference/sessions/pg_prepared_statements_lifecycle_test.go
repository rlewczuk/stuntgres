package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "errors"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferencePGPreparedStatementsLifecycle(t *testing.T) {
 sqlPreparedScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,a *sql.Conn) {
  peer,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer peer.Close();defer peer.ExecContext(context.Background(),"DEALLOCATE ALL")
  third,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer third.Close()
  first:="PREPARE pps_life AS SELECT 11";second:="PREPARE pps_life AS SELECT 22"
  sqlPreparedStep(t,p,b,a,fixture.Exec(first,0));sqlPreparedStep(t,p,b,peer,fixture.Exec(second,0))
  ppsSignature(t,p,b,a,"pps_life",first,"{}","{integer}",true);ppsSignature(t,p,b,peer,"pps_life",second,"{}","{integer}",true);ppsAbsent(t,p,b,third,"pps_life")
  original:=ppsCreated(t,p,a,"pps_life")
  sqlPreparedStep(t,p,b,a,fixture.Exec("DISCARD PLANS",0));if !ppsCreated(t,p,a,"pps_life").Equal(original) {t.Fatal("DISCARD PLANS changed timestamp")}
  sqlPreparedStep(t,p,b,a,fixture.Exec("DEALLOCATE pps_life",0));ppsAbsent(t,p,b,a,"pps_life")
  first="PREPARE pps_life AS SELECT 33";before:=ppsClock(t,p,a)
  sqlPreparedStep(t,p,b,a,fixture.Exec(first,0));created:=ppsCreated(t,p,a,"pps_life")
  if created.Before(before)||created.Before(original) {t.Fatal("replacement clock not admitted")}
  sqlPreparedStep(t,p,b,a,fixture.Exec("BEGIN",0));sqlPreparedStep(t,p,b,a,fixture.Exec("DEALLOCATE pps_life",0));sqlPreparedStep(t,p,b,a,fixture.Exec("ROLLBACK",0));ppsAbsent(t,p,b,a,"pps_life")
  sqlPreparedStep(t,p,b,a,fixture.Exec(first,0));sqlPreparedStep(t,p,b,a,fixture.Exec("DEALLOCATE ALL",0));ppsAbsent(t,p,b,a,"pps_life")
  sqlPreparedStep(t,p,b,a,fixture.Exec(first,0));sqlPreparedStep(t,p,b,a,fixture.Exec("DISCARD ALL",0));ppsAbsent(t,p,b,a,"pps_life")
  ppsSignature(t,p,b,peer,"pps_life",second,"{}","{integer}",true)
 })
 t.Run("physical_lifetime",func(t *testing.T) {
  p:=fixture.NewScenario(t)
  for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
   b.DB.SetMaxOpenConns(1);b.DB.SetMaxIdleConns(1)
   c,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)}
   source:="PREPARE pps_pool AS SELECT 19";sqlPreparedStep(t,p,b,c,fixture.Exec(source,0));created:=ppsCreated(t,p,c,"pps_pool")
   if err:=c.Close();err!=nil {t.Fatal(err)}
   c,err=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)}
   ppsSignature(t,p,b,c,"pps_pool",source,"{}","{integer}",true);if !ppsCreated(t,p,c,"pps_pool").Equal(created) {t.Fatal("borrower reset forgot provenance")}
   b.DB.SetMaxIdleConns(0);if err:=c.Close();err!=nil {t.Fatal(err)}
   c,err=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer c.Close();ppsAbsent(t,p,b,c,"pps_pool")
  })}
 })
 t.Run("native_controls",func(t *testing.T) {
  sqlPreparedScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   source:="PREPARE pps_control AS SELECT 17";sqlPreparedStep(t,p,b,c,fixture.Exec(source,0));created:=ppsCreated(t,p,c,"pps_control")
   if b.PG {ppsSignature(t,p,b,c,"pps_control",source,"{}","{integer}",true);return}
   peer,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer peer.Close();defer peer.ExecContext(p.Ctx,"DEALLOCATE ALL")
   other:="PREPARE pps_control AS SELECT 18";sqlPreparedStep(t,p,b,peer,fixture.Exec(other,0));otherCreated:=ppsCreated(t,p,peer,"pps_control")
   var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
   if strings.Contains(dump.String(),"pps_control")||strings.Contains(dump.String(),"PREPARE") {t.Fatal("dump retained session definitions")}
   for _,replace:=range []func()error{func()error{return stuntgres.Reset(p.Ctx,p.Native)},func()error{return stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()))}} {
    if err:=replace();err!=nil {t.Fatal(err)}
    ppsSignature(t,p,b,c,"pps_control",source,"{}","{integer}",true);ppsSignature(t,p,b,peer,"pps_control",other,"{}","{integer}",true)
    if !ppsCreated(t,p,c,"pps_control").Equal(created)||!ppsCreated(t,p,peer,"pps_control").Equal(otherCreated) {t.Fatal("catalog replacement changed provenance")}
   }
   fresh,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer fresh.Close();ppsAbsent(t,p,b,fresh,"pps_control")
  })
 })
}

func TestReferencePGPreparedStatementsCapture(t *testing.T) {
 sqlPreparedScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  sqlPreparedStep(t,p,b,c,fixture.Exec("CREATE SEQUENCE pps_seq START WITH 41",0))
  source:="PREPARE pps_volatile AS SELECT nextval('pps_seq')";sqlPreparedStep(t,p,b,c,fixture.Exec(source,0))
  created:=ppsCreated(t,p,c,"pps_volatile")
  for range 3 {ppsSignature(t,p,b,c,"pps_volatile",source,"{}","{bigint}",true)}
  sqlPreparedStep(t,p,b,c,sqlPreparedScalar("EXECUTE pps_volatile","nextval","INT8",int64(41)))
  if !ppsCreated(t,p,c,"pps_volatile").Equal(created) {t.Fatal("observation refreshed provenance")}
  if b.PG {return}
  ctx,cancel:=context.WithCancel(p.Ctx);cancel()
  if _,err:=c.ExecContext(ctx,"PREPARE pps_canceled AS SELECT 1");!errors.Is(err,context.Canceled) {t.Fatal(err)}
  if _,err:=c.ExecContext(ctx,"PREPARE pps_volatile AS SELECT 0");!errors.Is(err,context.Canceled) {t.Fatal(err)}
  rows,err:=c.QueryContext(ctx,"SELECT * FROM pg_prepared_statements");if rows!=nil {rows.Close();t.Fatal("canceled capture published rows")};if !errors.Is(err,context.Canceled) {t.Fatal(err)}
  ppsAbsent(t,p,b,c,"pps_canceled");ppsSignature(t,p,b,c,"pps_volatile",source,"{}","{bigint}",true)
  q:="SELECT name,statement,parameter_types::text,result_types::text,from_sql FROM pg_prepared_statements WHERE name='pps_volatile'"
  rows,err=c.QueryContext(context.WithValue(p.Ctx,fixture.RecorderKey{},b.Recorder),q);if err!=nil {t.Fatal(err)};defer rows.Close()
  frozen:=fixture.SequenceFrozenRows{Executor:c,Rows:rows,Query:q}
  sqlPreparedStep(t,p,b,c,fixture.Exec("DEALLOCATE pps_volatile",0));ppsAbsent(t,p,b,c,"pps_volatile")
  if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
  sqlPreparedStep(t,p,b,frozen,fixture.Select(q,[]string{"name","statement","parameter_types","result_types","from_sql"},[]string{"TEXT","TEXT","TEXT","TEXT","BOOL"},[][]any{{"pps_volatile",source,"{}","{bigint}",true}}))
 })
}

func TestReferencePGPreparedStatementsFeatureSmoke(t *testing.T) {
 sqlPreparedScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,a *sql.Conn) {
  peer,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer peer.Close();defer peer.ExecContext(context.Background(),"DEALLOCATE ALL")
  source:="PREPARE pps_smoke(int,text) AS SELECT $1 AS i,$2 AS s";before:=ppsClock(t,p,a)
  sqlPreparedStep(t,p,b,a,fixture.Exec(source,0));after:=ppsClock(t,p,a);created:=ppsCreated(t,p,a,"pps_smoke")
  if created.Before(before)||created.After(after) {t.Fatal("smoke prepare timestamp outside command bracket")}
  ppsSignature(t,p,b,a,"pps_smoke",source,"{integer,text}","{integer,text}",true)
  sqlPreparedStep(t,p,b,a,fixture.Select("SELECT * FROM pg_prepared_statements WHERE false",ppsColumns,ppsTypes,[][]any{}))
  sqlPreparedStep(t,p,b,a,fixture.Exec("SET plan_cache_mode=force_custom_plan",0))
  sqlPreparedStep(t,p,b,a,fixture.Select("EXECUTE pps_smoke(7,'seven')",[]string{"i","s"},[]string{"INT4","TEXT"},[][]any{{int64(7),"seven"}}))
  var generic,custom any;if b.PG {generic=int64(0);custom=int64(1)}
  sqlPreparedStep(t,p,b,a,fixture.Select("SELECT generic_plans,custom_plans FROM pg_prepared_statements WHERE name='pps_smoke'",[]string{"generic_plans","custom_plans"},[]string{"INT8","INT8"},[][]any{{generic,custom}}))
  if !ppsCreated(t,p,a,"pps_smoke").Equal(created) {t.Fatal("smoke execution changed provenance")}
  other:="PREPARE pps_smoke AS SELECT 22";sqlPreparedStep(t,p,b,peer,fixture.Exec(other,0));ppsSignature(t,p,b,peer,"pps_smoke",other,"{}","{integer}",true);ppsSignature(t,p,b,a,"pps_smoke",source,"{integer,text}","{integer,text}",true)
  sqlPreparedStep(t,p,b,a,fixture.Exec("DEALLOCATE pps_smoke",0));ppsAbsent(t,p,b,a,"pps_smoke")
  sqlPreparedStep(t,p,b,a,fixture.Exec(source,0));sqlPreparedStep(t,p,b,a,fixture.Exec("DISCARD ALL",0));ppsAbsent(t,p,b,a,"pps_smoke");ppsSignature(t,p,b,peer,"pps_smoke",other,"{}","{integer}",true)
  sqlPreparedStep(t,p,b,a,fixture.Select("SELECT * FROM pg_prepared_statements WHERE false",ppsColumns,ppsTypes,[][]any{}))
 })
}
