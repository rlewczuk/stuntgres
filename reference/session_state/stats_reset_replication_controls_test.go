package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "reflect"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceStatsResetReplicationControls(t *testing.T) {
 fixture.RoleReferenceCleanup(t,[]string{"reset_control_reader"})
 activityScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  bootstrap:="postgres"; if b.PG { bootstrap="stuntgres" }
  activityExec(t,p,c,"CREATE ROLE reset_control_reader")
  defer c.ExecContext(context.Background(),"RESET ROLE")
  oid:=resetReplicationSubscription(t,p,c,"reset_controls_subscription")
  subscriptionBefore:=resetReplicationDescriptor(t,p,c,oid)
  defer func(){ c.ExecContext(context.Background(),"RESET ROLE"); c.ExecContext(context.Background(),"DROP SUBSCRIPTION IF EXISTS reset_controls_subscription") }()
  for _,f:=range resetReplicationFunctions {
   identity:="pg_catalog."+f.name+"("+f.argument+")"
   query:="SELECT pg_catalog."+f.name+"(NULL::"+f.argument+")"
   for _,state:=range []string{"bootstrap","granted","empty"} {
    switch state {
    case "granted": activityExec(t,p,c,"GRANT EXECUTE ON FUNCTION "+identity+" TO reset_control_reader WITH GRANT OPTION")
    case "empty": activityExec(t,p,c,"REVOKE EXECUTE ON FUNCTION "+identity+" FROM reset_control_reader"); activityExec(t,p,c,"REVOKE EXECUTE ON FUNCTION "+identity+" FROM "+bootstrap)
    }
    var before string
    if err:=c.QueryRowContext(p.Ctx,"SELECT proacl::text FROM pg_proc WHERE oid=$1::oid",f.oid).Scan(&before); err!=nil { t.Fatal(err) }
    expected:="{"+bootstrap+"=X/"+bootstrap+"}"; if state=="granted" { expected="{"+bootstrap+"=X/"+bootstrap+",reset_control_reader=X*/"+bootstrap+"}" }; if state=="empty" { expected="{}" }
    if before!=expected { t.Fatal(state,before,expected) }
    if !b.PG {
     var first,second bytes.Buffer
     if err:=stuntgres.Dump(p.Ctx,p.Native,&first); err!=nil { t.Fatal(err) }
     if err:=stuntgres.Dump(p.Ctx,p.Native,&second); err!=nil || !bytes.Equal(first.Bytes(),second.Bytes()) { t.Fatal("nondeterministic dump",err) }
     if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader("CREATE TABLE broken(")); err==nil { t.Fatal("invalid restore succeeded") }
     var after string
     if err:=c.QueryRowContext(p.Ctx,"SELECT proacl::text FROM pg_proc WHERE oid=$1::oid",f.oid).Scan(&after); err!=nil || after!=before { t.Fatal("failed restore mutated ACL",after,before,err) }
     if err:=stuntgres.Reset(p.Ctx,p.Native); err!=nil { t.Fatal(err) }
     if err:=c.QueryRowContext(p.Ctx,"SELECT proacl::text FROM pg_proc WHERE oid=$1::oid",f.oid).Scan(&after); err!=nil || after!="{postgres=X/postgres}" { t.Fatal("reset did not reconstruct bootstrap",after,err) }
     var initial string
     if err:=c.QueryRowContext(p.Ctx,"SELECT initprivs::text FROM pg_init_privs WHERE objoid=$1::oid AND classoid='pg_proc'::regclass AND objsubid=0 AND privtype='i'",f.oid).Scan(&initial); err!=nil || initial!="{postgres=X/postgres}" { t.Fatal("Reset lost initial privilege baseline",initial,err) }
     if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(first.Bytes())); err!=nil { t.Fatal(err) }
     if err:=c.QueryRowContext(p.Ctx,"SELECT proacl::text FROM pg_proc WHERE oid=$1::oid",f.oid).Scan(&after); err!=nil || after!=before { t.Fatal("restore lost raw ACL",after,before,err) }
     var restoredOID int64
     if err:=c.QueryRowContext(p.Ctx,"SELECT oid::bigint FROM pg_subscription WHERE subname='reset_controls_subscription'").Scan(&restoredOID); err!=nil { t.Fatal(err) }
     if afterSubscription:=resetReplicationDescriptor(t,p,c,restoredOID); !reflect.DeepEqual(afterSubscription[3:],subscriptionBefore[3:]) { t.Fatal("subscription metadata replay",afterSubscription,subscriptionBefore) }
    }
    activityExec(t,p,c,"SET ROLE reset_control_reader")
    if state=="granted" { resetReplicationVoid(t,p,b,c,query,f.name) } else {
     step:=fixture.ErrorDiagnostic(query,activityDiagnostic("42501","permission denied for function "+f.name,0)); step.SelectRows=true
     fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
    }
    activityExec(t,p,c,"RESET ROLE")
   }
   activityExec(t,p,c,"GRANT EXECUTE ON FUNCTION "+identity+" TO "+bootstrap)
  }
 })
}

func TestReferenceStatsResetReplicationSmoke(t *testing.T) {
 fixture.RoleReferenceCleanup(t,[]string{"reset_smoke_reader"})
 activityScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  resetReplicationVoid(t,p,b,c,"SELECT pg_stat_reset_replication_slot(NULL::text)","pg_stat_reset_replication_slot")
  oid:=resetReplicationSubscription(t,p,c,"reset_smoke_subscription")
  defer c.ExecContext(context.Background(),"DROP SUBSCRIPTION IF EXISTS reset_smoke_subscription")
  before:=resetReplicationDescriptor(t,p,c,oid)
  query:="SELECT pg_stat_reset_subscription_stats($1::oid)"
  stmt,err:=c.PrepareContext(p.Ctx,query); if err!=nil { t.Fatal(err) }; defer stmt.Close()
  held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:query}
  for _,arg:=range []any{nil,int64(4294967294),oid} { resetReplicationVoid(t,p,b,held,query,"pg_stat_reset_subscription_stats",arg) }
  after:=resetReplicationDescriptor(t,p,c,oid)
  for i:=range before { if before[i]!=after[i] { t.Fatal("reset changed descriptor",before,after) } }
  step:=fixture.ErrorDiagnostic(query,activityDiagnostic("22023","invalid subscription OID 0",0),int64(0)); step.SelectRows=true
  fixture.RunStep(t,p.Ctx,held,b.Recorder,step)
  resetReplicationRuntimeError(t,p,b,c,"SELECT pg_stat_reset_replication_slot('reset_missing_slot')",activityDiagnostic("22023",`replication slot "reset_missing_slot" does not exist`,0))
  activityExec(t,p,c,"CREATE ROLE reset_smoke_reader")
  defer c.ExecContext(context.Background(),"RESET ROLE")
  identity:="pg_catalog.pg_stat_reset_subscription_stats(oid)"
  activityExec(t,p,c,"SET ROLE reset_smoke_reader")
  denied:=fixture.ErrorDiagnostic(query,activityDiagnostic("42501","permission denied for function pg_stat_reset_subscription_stats",0),nil); denied.SelectRows=true
  fixture.RunStep(t,p.Ctx,held,b.Recorder,denied)
  activityExec(t,p,c,"RESET ROLE"); activityExec(t,p,c,"GRANT EXECUTE ON FUNCTION "+identity+" TO reset_smoke_reader")
  defer func(){ c.ExecContext(context.Background(),"RESET ROLE"); c.ExecContext(context.Background(),"REVOKE EXECUTE ON FUNCTION "+identity+" FROM reset_smoke_reader") }()
  activityExec(t,p,c,"SET ROLE reset_smoke_reader"); resetReplicationVoid(t,p,b,held,query,"pg_stat_reset_subscription_stats",nil)
  activityExec(t,p,c,"RESET ROLE"); activityExec(t,p,c,"REVOKE EXECUTE ON FUNCTION "+identity+" FROM reset_smoke_reader")
  activityExec(t,p,c,"SET ROLE reset_smoke_reader"); fixture.RunStep(t,p.Ctx,held,b.Recorder,denied)
 })
}
