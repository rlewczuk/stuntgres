package reference_test

import (
 "context"
 "fmt"
 "stuntgres/reference/fixture"
 "testing"
)

func TestReferenceReplicationOriginReservationBoundaries(t *testing.T) {
 fixture.RoleReferenceCleanup(t,[]string{"origin_settler_denied"})
 p:=subscriptionScenario(t,"origin_boundary","origin_boundary_peer","origin_boundary_suffix","origin_exception_outer","origin_exception_inner")
 for _,b:=range p.Backends() { t.Run(b.Name,func(t *testing.T) {
  actor,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
  defer actor.ExecContext(context.Background(),"ROLLBACK")
  defer actor.ExecContext(context.Background(),"RESET ROLE")
  subscriptionExec(t,p,b,observer,"CREATE ROLE origin_settler_denied",false)
  for _,commit:=range []bool{false,true} {
   gid:=fmt.Sprintf("origin_prepared_%t",commit)
   fixture.PreparedReferenceCleanup(t,b,gid)
   baseline:=originBag(t,p,observer)
   subscriptionExec(t,p,b,actor,"BEGIN",false)
   originCreate(t,p,b,actor,"origin_boundary",false)
   id,name:=originIdentity(t,p,actor,"origin_boundary")
   subscriptionExec(t,p,b,actor,"PREPARE TRANSACTION '"+gid+"'",false)
   originCheck(t,p,b,observer,"origin_boundary",0,"")
   fixture.PreparedReferenceOne(t,p,observer,gid)
   subscriptionExec(t,p,b,actor,"SET ROLE origin_settler_denied",false)
   for _,word:=range []string{"COMMIT","ROLLBACK"} {
    want:=fixture.ControlsReferenceDiagnostic("42501","permission denied to finish prepared transaction")
    want.Hint="Must be superuser or the user that prepared the transaction."
    step:=fixture.ErrorDiagnostic(word+" PREPARED '"+gid+"'",want);step.ErrorStage="execute"
    subscriptionRun(t,p,b,actor,step)
    fixture.PreparedReferenceOne(t,p,observer,gid)
   }
   subscriptionExec(t,p,b,actor,"RESET ROLE",false)
   subscriptionExec(t,p,b,actor,"DISCARD ALL",false)
   fixture.PreparedReferenceOne(t,p,observer,gid)
   // Detached originating transaction/session no longer owns the registration;
   // the prepared transaction keeps allocation exclusion until settlement.
   originCreate(t,p,b,observer,"origin_boundary_peer",false)
   peerID,_:=originIdentity(t,p,observer,"origin_boundary_peer")
   if peerID==id { t.Fatal("prepared origin ID was handed to another transaction") }
   ending:="ROLLBACK";if commit { ending="COMMIT" }
   subscriptionExec(t,p,b,observer,ending+" PREPARED '"+gid+"'",false)
   if commit { originCheck(t,p,b,observer,"origin_boundary",id,name);subscriptionExec(t,p,b,observer,"DROP SUBSCRIPTION origin_boundary",false) } else { originCheck(t,p,b,observer,"origin_boundary",0,"") }
   subscriptionExec(t,p,b,observer,"DROP SUBSCRIPTION origin_boundary_peer",false)
   originCreate(t,p,b,observer,"origin_boundary",false)
   reused,_:=originIdentity(t,p,observer,"origin_boundary")
   if reused!=originFirstFree(baseline) { t.Fatal("prepared completion did not release lowest ID",reused,baseline) }
   subscriptionExec(t,p,b,observer,"DROP SUBSCRIPTION origin_boundary",false)
  }
  for _,commit:=range []bool{false,true} {
   gid:=fmt.Sprintf("origin_drop_prepared_%t",commit)
   fixture.PreparedReferenceCleanup(t,b,gid)
   originCreate(t,p,b,observer,"origin_boundary",false)
   id,name:=originIdentity(t,p,observer,"origin_boundary")
   subscriptionExec(t,p,b,actor,"BEGIN",false)
   subscriptionExec(t,p,b,actor,"DROP SUBSCRIPTION origin_boundary",false)
   subscriptionExec(t,p,b,actor,"PREPARE TRANSACTION '"+gid+"'",false)
   originCheck(t,p,b,observer,"origin_boundary",id,name)
   ending:="ROLLBACK";if commit { ending="COMMIT" }
   subscriptionExec(t,p,b,observer,ending+" PREPARED '"+gid+"'",false)
   if commit { originCheck(t,p,b,observer,"origin_boundary",0,"") } else {
    originCheck(t,p,b,observer,"origin_boundary",id,name)
    subscriptionExec(t,p,b,observer,"DROP SUBSCRIPTION origin_boundary",false)
   }
  }
  subscriptionExec(t,p,b,observer,"DROP ROLE origin_settler_denied",false)
  // A drop whose predecessor is retained by a savepoint must not free the
  // uncommitted create's ID for another transaction.
  subscriptionExec(t,p,b,actor,"BEGIN",false)
  originCreate(t,p,b,actor,"origin_boundary",false)
  id,name:=originIdentity(t,p,actor,"origin_boundary")
  subscriptionExec(t,p,b,actor,"SAVEPOINT revive",false)
  subscriptionExec(t,p,b,actor,"DROP SUBSCRIPTION origin_boundary",false)
  // PostgreSQL DROP retains RowExclusiveLock on pg_replication_origin until
  // subtransaction rollback, which legitimately blocks CREATE's ExclusiveLock.
  // Native exclusion ownership is independently observable before restoration.
  if !b.PG {
   originCreate(t,p,b,observer,"origin_boundary_peer",false)
   peerID,_:=originIdentity(t,p,observer,"origin_boundary_peer");if peerID==id { t.Fatal("revivable ID stolen") }
  }
  subscriptionExec(t,p,b,actor,"ROLLBACK TO revive",false)
  if b.PG {
   originCreate(t,p,b,observer,"origin_boundary_peer",false)
   peerID,_:=originIdentity(t,p,observer,"origin_boundary_peer");if peerID==id { t.Fatal("restored ID stolen") }
  }
  originCheck(t,p,b,actor,"origin_boundary",id,name)
  subscriptionExec(t,p,b,actor,"ROLLBACK AND CHAIN",false)
  originCheck(t,p,b,actor,"origin_boundary",0,"")
  originCreate(t,p,b,actor,"origin_boundary_suffix",false)
  suffix,_:=originIdentity(t,p,actor,"origin_boundary_suffix");if suffix!=id { t.Fatal("CHAIN retained old incarnation reservation",suffix,id) }
  subscriptionExec(t,p,b,actor,"COMMIT AND CHAIN",false)
  originCheck(t,p,b,observer,"origin_boundary_suffix",suffix,fmt.Sprint("pg_",subscriptionNumber(t,p,observer,"SELECT oid::bigint FROM pg_subscription WHERE subname='origin_boundary_suffix'")))
  subscriptionExec(t,p,b,actor,"ROLLBACK",false)
  subscriptionExec(t,p,b,observer,"DROP SUBSCRIPTION origin_boundary_suffix",false)
  subscriptionExec(t,p,b,observer,"DROP SUBSCRIPTION origin_boundary_peer",false)
  subscriptionExec(t,p,b,actor,"DO $$BEGIN EXECUTE 'CREATE SUBSCRIPTION origin_exception_outer CONNECTION '''' PUBLICATION p WITH (connect=false,slot_name=NONE)'; BEGIN EXECUTE 'CREATE SUBSCRIPTION origin_exception_inner CONNECTION '''' PUBLICATION p WITH (connect=false,slot_name=NONE)'; RAISE EXCEPTION 'discard inner'; EXCEPTION WHEN OTHERS THEN PERFORM 1; END; END$$",false)
  outer,outerName:=originIdentity(t,p,observer,"origin_exception_outer")
  originCheck(t,p,b,observer,"origin_exception_outer",outer,outerName)
  originCheck(t,p,b,observer,"origin_exception_inner",0,"")
  free:=originFirstFree(originBag(t,p,observer))
  originCreate(t,p,b,observer,"origin_exception_inner",false)
  inner,_:=originIdentity(t,p,observer,"origin_exception_inner");if inner!=free || inner==outer { t.Fatal("exception suffix reservation cleanup",inner,free,outer) }
  subscriptionExec(t,p,b,observer,"DROP SUBSCRIPTION origin_exception_inner",false)
  subscriptionExec(t,p,b,observer,"DROP SUBSCRIPTION origin_exception_outer",false)
 }) }
}
