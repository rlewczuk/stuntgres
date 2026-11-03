package reference_test

import (
 "context"
 "database/sql"
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

func resetReplicationSubscription(t *testing.T,p *fixture.Pair,c *sql.Conn,name string) int64 {
 t.Helper()
 activityExec(t,p,c,"CREATE SUBSCRIPTION "+name+" CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)")
 var oid int64
 if err:=c.QueryRowContext(p.Ctx,"SELECT oid::bigint FROM pg_subscription WHERE subname=$1",name).Scan(&oid); err!=nil || oid<=0 { t.Fatal(oid,err) }
 return oid
}
func resetReplicationDescriptor(t *testing.T,p *fixture.Pair,c *sql.Conn,oid int64) []any {
 t.Helper()
 row:=make([]any,8); pointers:=make([]any,len(row)); for i:=range row { pointers[i]=&row[i] }
 if err:=c.QueryRowContext(p.Ctx,"SELECT oid::bigint,subdbid::bigint,subowner::bigint,subname::text,subenabled,subslotname::text,subconninfo,subpublications::text FROM pg_subscription WHERE oid=$1::oid",oid).Scan(pointers...); err!=nil { t.Fatal(err) }
 for i:=range row { row[i]=fixture.Normalize(row[i]) }; return row
}
func TestReferenceStatsResetReplicationSubscriptions(t *testing.T) {
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends() { t.Run(b.Name,func(t *testing.T) {
  var notices []fixture.Diagnostic
  _,c,recorder:=fixture.NoticeReferencePool(t,p,b.PG,&notices)
  b.Recorder=recorder
  names:=[]string{"reset_actual_a","reset_actual_b"}
  oids:=make([]int64,2)
  for i,name:=range names { oids[i]=resetReplicationSubscription(t,p,c,name); defer c.ExecContext(context.Background(),"DROP SUBSCRIPTION IF EXISTS "+name) }
  warning:=fixture.Diagnostic{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:"01000",Message:"subscription was created, but is not connected",Hint:"To initiate replication, you must manually create the replication slot, enable the subscription, and refresh the subscription."}
  if !reflect.DeepEqual(notices,[]fixture.Diagnostic{warning,warning}) { t.Fatal("disconnected creation diagnostics",notices) }
  activityExec(t,p,c,"CREATE DATABASE reset_stats_peer")
  defer c.ExecContext(context.Background(),"DROP DATABASE IF EXISTS reset_stats_peer")
  pool,_,recorder:=fixture.DatabaseScenarioPool(t,p,b.PG,"reset_stats_peer","")
  peer,err:=pool.Conn(p.Ctx); if err!=nil { t.Fatal(err) }
  defer pool.Close(); defer peer.Close()
  peerOID:=resetReplicationSubscription(t,p,peer,"reset_actual_c")
  defer peer.ExecContext(context.Background(),"DROP SUBSCRIPTION IF EXISTS reset_actual_c")
  if peerOID==oids[0] || peerOID==oids[1] || oids[0]==oids[1] { t.Fatal("cluster identities collided",oids,peerOID) }
  all:=append(append([]int64{},oids...),peerOID)
  before:=make([][]any,len(all)); for i,oid:=range all { before[i]=resetReplicationDescriptor(t,p,c,oid) }
  query:="SELECT pg_stat_reset_subscription_stats($1::oid)"
  for _,entry:=range []struct{c *sql.Conn; b fixture.Backend}{{c,b},{peer,fixture.Backend{Name:b.Name,DB:pool,Recorder:recorder,PG:b.PG}}} {
   stmt,err:=entry.c.PrepareContext(p.Ctx,query); if err!=nil { t.Fatal(err) }
   held:=fixture.HeldStatement{Executor:entry.c,Statement:stmt,SQL:query}
   for _,oid:=range all { resetReplicationVoid(t,p,entry.b,entry.c,query,"pg_stat_reset_subscription_stats",oid); resetReplicationVoid(t,p,entry.b,held,query,"pg_stat_reset_subscription_stats",oid) }
   resetReplicationVoid(t,p,entry.b,held,query,"pg_stat_reset_subscription_stats",nil)
   resetReplicationVoid(t,p,entry.b,entry.c,query,"pg_stat_reset_subscription_stats",nil)
   stmt.Close()
  }
  for i,oid:=range all { if after:=resetReplicationDescriptor(t,p,c,oid); !reflect.DeepEqual(before[i],after) { t.Fatal("reset mutated descriptor",before[i],after) } }
  activityExec(t,p,c,"ALTER SUBSCRIPTION reset_actual_a RENAME TO reset_actual_renamed")
  defer c.ExecContext(context.Background(),"DROP SUBSCRIPTION IF EXISTS reset_actual_renamed")
  activityExec(t,p,c,"ALTER SUBSCRIPTION reset_actual_renamed SET (binary=true)")
  if after:=resetReplicationDescriptor(t,p,c,oids[0]); after[0]!=oids[0] { t.Fatal("rename reminted subscription",after) }
  resetReplicationVoid(t,p,b,c,query,"pg_stat_reset_subscription_stats",oids[0])
  resetReplicationVoid(t,p,b,c,query,"pg_stat_reset_subscription_stats",int64(4294967294))
  resetReplicationRuntimeError(t,p,b,c,query,activityDiagnostic("22023","invalid subscription OID 0",0),int64(0))
 }) }
}
