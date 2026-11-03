package reference_test

import (
 "context"
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceStatsResetReplicationPrivileges(t *testing.T) {
 roles:=[]string{"reset_stats_reader","reset_stats_group","reset_stats_replication","reset_stats_owner"}
 fixture.RoleReferenceCleanup(t,roles)
 activityScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  bootstrap:="postgres"; if b.PG { bootstrap="stuntgres" }
  for _,role:=range roles { suffix:=""; if role=="reset_stats_replication" { suffix=" REPLICATION" }; activityExec(t,p,c,"CREATE ROLE "+role+suffix) }
  defer c.ExecContext(context.Background(),"RESET ROLE")
  activityExec(t,p,c,"CREATE SUBSCRIPTION reset_stats_owned CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)")
  defer func(){ c.ExecContext(context.Background(),"RESET ROLE"); c.ExecContext(context.Background(),"DROP SUBSCRIPTION IF EXISTS reset_stats_owned") }()
  activityExec(t,p,c,"GRANT pg_create_subscription TO reset_stats_owner")
  activityExec(t,p,c,"ALTER SUBSCRIPTION reset_stats_owned OWNER TO reset_stats_owner")
  activityExec(t,p,c,"GRANT pg_monitor,pg_read_all_stats TO reset_stats_reader")
  for _,f:=range resetReplicationFunctions {
   identity:="pg_catalog."+f.name+"("+f.argument+")"
   // Initial privilege history is independent of mutable live grants.
   var live,initial string
   if err:=c.QueryRowContext(p.Ctx,"SELECT proacl::text FROM pg_proc WHERE proname=$1 AND pronamespace='pg_catalog'::regnamespace",f.name).Scan(&live); err!=nil { t.Fatal(err) }
   if err:=c.QueryRowContext(p.Ctx,"SELECT initprivs::text FROM pg_init_privs WHERE objoid=$1::oid AND classoid='pg_proc'::regclass AND objsubid=0 AND privtype='i'",f.oid).Scan(&initial); err!=nil { t.Fatal(err) }
   if live!="{"+bootstrap+"=X/"+bootstrap+"}" || initial!=live { t.Fatal("restricted bootstrap ACL",live,initial) }
   query:="SELECT pg_catalog."+f.name+"($1::"+f.argument+")"
   stmt,err:=c.PrepareContext(p.Ctx,query); if err!=nil { t.Fatal(err) }
   held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:query}
   denied:=activityDiagnostic("42501","permission denied for function "+f.name,0)
   deny:=func(argument any) {
    step:=fixture.ErrorDiagnostic(query,denied,argument); step.SelectRows=true
    fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
    fixture.RunStep(t,p.Ctx,held,b.Recorder,step)
   }
   for _,role:=range []string{"reset_stats_reader","reset_stats_replication","reset_stats_owner"} {
    activityExec(t,p,c,"SET ROLE "+role); deny(nil)
    if f.argument=="oid" { deny(int64(0)); deny(int64(4294967294)) } else { deny("reset_missing_slot") }
    activityExec(t,p,c,"RESET ROLE")
   }
   activityExec(t,p,c,"GRANT EXECUTE ON FUNCTION "+identity+" TO reset_stats_reader")
   activityExec(t,p,c,"SET ROLE reset_stats_reader")
   resetReplicationVoid(t,p,b,held,query,f.name,nil)
   activityExec(t,p,c,"RESET ROLE")
   activityExec(t,p,c,"REVOKE EXECUTE ON FUNCTION "+identity+" FROM reset_stats_reader")
   activityExec(t,p,c,"SET ROLE reset_stats_reader"); deny(nil); activityExec(t,p,c,"RESET ROLE")
   activityExec(t,p,c,"GRANT EXECUTE ON FUNCTION "+identity+" TO reset_stats_group")
   activityExec(t,p,c,"GRANT reset_stats_group TO reset_stats_reader")
   activityExec(t,p,c,"SET ROLE reset_stats_reader"); resetReplicationVoid(t,p,b,held,query,f.name,nil); activityExec(t,p,c,"RESET ROLE")
   activityExec(t,p,c,"REVOKE reset_stats_group FROM reset_stats_reader")
   activityExec(t,p,c,"SET ROLE reset_stats_reader"); deny(nil); activityExec(t,p,c,"RESET ROLE")
   activityExec(t,p,c,"REVOKE EXECUTE ON FUNCTION "+identity+" FROM reset_stats_group")
   if err:=stmt.Close(); err!=nil { t.Fatal(err) }
  }
 })
}
