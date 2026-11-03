package core_test

import (
 "bytes"
 "context"
 "errors"
 "fmt"
 "strings"
 "testing"
 "time"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/statistics"
 "stuntgres/internal/parser"
)

func TestStatsResetReplicationLifetime(t *testing.T) {
 for _,kind:=range []string{"commit","abort","failed_statement","unadopted_stage","savepoint_discarded","savepoint_retained","released_child_abort","prepared_commit","prepared_abort","create_drop_commit","create_drop_abort","reset_abort"} { t.Run("creation/"+kind,func(t *testing.T) {
  e:=engine.New(); s:=enginetest.StartSession(t,e,nil)
  statsFunctionExec(t,s,"BEGIN")
  if kind=="savepoint_discarded" || kind=="released_child_abort" { statsFunctionExec(t,s,"SAVEPOINT creation") }
  var key statistics.Key
  if kind=="failed_statement" || kind=="unadopted_stage" {
   scope,stage,d,done:=originStage(t,e,s,"reset_created")
   key=statistics.Key{Object:e.Catalog.Domain.Ref(stage.StageRead().Epoch(),d.Address()),Category:statistics.Subscription}
   var reader statistics.Reader; reader.Configure("snapshot",1); reader.Read(&e.Statistics,key,time.Now())
   if !statsLifetimeSnapshotContains(&reader,key) { t.Fatal("provisional creation missing shared record") }
   resetStatsSeed(e,key,1)
   if kind=="failed_statement" { transferOriginStage(t,e,scope,stage); failure:=errors.New("statement publication veto"); if err:=scope.Finish(failure); !errors.Is(err,failure) { t.Fatal(err) } } else { e.DiscardCatalogStage(stage); scope.Finish(context.Canceled) }
   done(); statsFunctionExec(t,s,"ROLLBACK"); resetStatsAbsent(t,e,key); return
  }
  key=resetStatsCreate(t,e,s,"reset_created"); before:=resetStatsSeed(e,key,1)
  var retained statistics.Reader; retained.Configure("snapshot",1); retained.Read(&e.Statistics,key,time.Now())
  var late statistics.Writer; late.Add(&e.Statistics,key,resetStatsVector(21))
  keep:=kind=="commit" || kind=="savepoint_retained" || kind=="prepared_commit"
  switch kind {
  case "savepoint_discarded": statsFunctionExec(t,s,"ROLLBACK TO creation","ROLLBACK TO creation","COMMIT")
  case "savepoint_retained": statsFunctionExec(t,s,"SAVEPOINT creation","ALTER SUBSCRIPTION reset_created SET (binary=true)","DROP SUBSCRIPTION reset_created","ROLLBACK TO creation","ROLLBACK TO creation","COMMIT")
  case "released_child_abort":
   statsFunctionExec(t,s,"RELEASE creation")
   if e.Statistics.Read(key)!=before { t.Fatal("child RELEASE retired live creation") }
   statsFunctionExec(t,s,"ROLLBACK")
  case "prepared_commit","prepared_abort": statsLifetimeFinishPrepared(t,e,s,keep,func(){ if e.Statistics.Read(key)!=before { t.Fatal("PREPARE retired live creation") } })
  case "create_drop_commit","create_drop_abort":
   statsFunctionExec(t,s,"DROP SUBSCRIPTION reset_created"); if e.Statistics.Read(key)!=before { t.Fatal("staged drop retired creation") }
   end:="ROLLBACK"; if kind=="create_drop_commit" { end="COMMIT" }; statsFunctionExec(t,s,end)
  case "reset_abort":
   resetStatsExec(t,s,fmt.Sprintf("SELECT pg_stat_reset_subscription_stats(%d::oid)",key.Object.Address.ObjectID)); resetStatsZero(t,e,key); statsFunctionExec(t,s,"ROLLBACK")
  default: end:="ROLLBACK"; if keep { end="COMMIT" }; statsFunctionExec(t,s,end)
  }
  if retained.Read(&e.Statistics,key,time.Now())!=before { t.Fatal("lifetime mutated retained reader") }
  if keep { if got:=e.Statistics.Read(key); got!=before { t.Fatal("retained creation lost history",got,before) } } else {
   late.Flush(&e.Statistics,time.Now(),true); late.Add(&e.Statistics,key,resetStatsVector(31)); late.Flush(&e.Statistics,time.Now(),true); resetStatsAbsent(t,e,key)
  }
 }) }
 for _,kind:=range []string{"commit","abort","savepoint_abort","released_child_abort","released_child_commit","prepared_commit","prepared_abort","direct_nil_operation","rename_options","recreate"} { t.Run("existing/"+kind,func(t *testing.T) {
  e:=engine.New(); s:=enginetest.StartSession(t,e,nil); key:=resetStatsCreate(t,e,s,"reset_existing"); before:=resetStatsSeed(e,key,1)
  survivor:=resetStatsCreate(t,e,s,"reset_survivor"); control:=resetStatsSeed(e,survivor,11)
  var late statistics.Writer; late.Add(&e.Statistics,key,resetStatsVector(21))
  retired:=kind=="commit" || kind=="released_child_commit" || kind=="prepared_commit" || kind=="direct_nil_operation" || kind=="recreate"
  if kind=="direct_nil_operation" {
   base:=e.CatalogRead(); stage:=e.BeginCatalogStage(base); defer e.DiscardCatalogStage(stage)
   op,err:=s.Acquire(context.Background()); if err!=nil { t.Fatal(err) }
   e.Mu.Lock()
   auth,err:=e.AuthorizationLocked(op)
   if err==nil { stage.AuthorizeLocked(auth,s); err=stage.RemoveObjectLocked(auth.Ref(key.Object.Address),false) }
   e.Mu.Unlock(); op.Done()
   if err!=nil { t.Fatal(err) }
   prepared,err:=e.ValidateCatalogStage(context.Background(),stage); if err!=nil { t.Fatal(err) }
   if e.Statistics.Read(key)!=before { t.Fatal("validation retired existing entry") }
   if _,err:=enginetest.PublishCatalog(e,prepared,core.PublicationToken{Epoch:base.Epoch()}); err!=nil { t.Fatal(err) }
  } else if kind=="rename_options" {
   statsFunctionExec(t,s,"ALTER SUBSCRIPTION reset_existing RENAME TO reset_renamed","ALTER SUBSCRIPTION reset_renamed SET (binary=true)","CREATE ROLE reset_history_owner","ALTER SUBSCRIPTION reset_renamed OWNER TO reset_history_owner")
   if resetStatsKey(t,e,s,"reset_renamed")!=key { t.Fatal("compatible alter changed actual key") }
  } else {
   statsFunctionExec(t,s,"BEGIN")
   if strings.Contains(kind,"savepoint") || strings.Contains(kind,"released_child") { statsFunctionExec(t,s,"SAVEPOINT dropped") }
   statsFunctionExec(t,s,"DROP SUBSCRIPTION reset_existing")
   if e.Statistics.Read(key)!=before { t.Fatal("staged drop retired existing shared entry") }
   switch kind {
   case "prepared_commit","prepared_abort": statsLifetimeFinishPrepared(t,e,s,retired,func(){ if e.Statistics.Read(key)!=before { t.Fatal("PREPARE retired staged drop") } })
   case "savepoint_abort": statsFunctionExec(t,s,"ROLLBACK TO dropped","COMMIT")
   case "released_child_abort","released_child_commit":
    end:="ROLLBACK"; if retired { end="COMMIT" }
    statsFunctionExec(t,s,"RELEASE dropped")
    if e.Statistics.Read(key)!=before { t.Fatal("child RELEASE retired staged drop") }
    statsFunctionExec(t,s,end)
   case "abort": statsFunctionExec(t,s,"ROLLBACK")
   case "recreate":
    successor:=resetStatsCreate(t,e,s,"reset_existing"); if successor==key { t.Fatal("drop/recreate reused exact identity") }; statsFunctionExec(t,s,"COMMIT")
    if got:=e.Statistics.Read(successor); !got.ResetAt.IsZero() || got.ApplyErrorCount!=0 { t.Fatal("successor inherited history",got) }
   default: statsFunctionExec(t,s,"COMMIT")
   }
  }
  if retired { late.Flush(&e.Statistics,time.Now(),true); late.Add(&e.Statistics,key,resetStatsVector(31)); late.Flush(&e.Statistics,time.Now(),true); resetStatsAbsent(t,e,key) } else if e.Statistics.Read(key)!=before { t.Fatal("retained identity lost history",e.Statistics.Read(key),before) }
  if e.Statistics.Read(survivor)!=control { t.Fatal("retirement changed unrelated subscription") }
 }) }
 t.Run("pl_checkpoint",resetStatsPLCheckpoint)
 t.Run("native_replacement",resetStatsReplacement)
 t.Run("raw_acl_replay",resetStatsACLReplay)
}

func resetStatsPLCheckpoint(t *testing.T) {
 e:=engine.New(); s:=enginetest.StartSession(t,e,nil); statsFunctionExec(t,s,"BEGIN")
 scope,_:=truncateUnitScope(t,e,s,context.Background())
 if err:=scope.Gate(&parser.Do{}); err!=nil { t.Fatal(err) }
 mark,err:=scope.BeginPLException(); if err!=nil { t.Fatal(err) }
 restore:=scope.ActivateSQLRoutine(nil,e.CatalogRead())
 result,err:=scope.Execute(enginetest.Statement(t,"CREATE SUBSCRIPTION reset_pl_discarded CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)"),0,nil)
 if result.Rows!=nil { result.Rows.Close() }; if err=restore(err); err!=nil { t.Fatal(err) }
 key:=resetStatsKey(t,e,s,"reset_pl_discarded",scope.Catalog,s.Transactions.Catalog); resetStatsSeed(e,key,1)
 var late statistics.Writer; late.Add(&e.Statistics,key,resetStatsVector(11))
 if err:=scope.EndPLException(mark,true,nil); err!=nil { t.Fatal(err) }
 if err:=scope.Finish(nil); err!=nil { t.Fatal(err) }; scope.Op.Done()
 statsFunctionExec(t,s,"COMMIT"); late.Flush(&e.Statistics,time.Now(),true); resetStatsAbsent(t,e,key)
}

func resetStatsReplacement(t *testing.T) {
 e,s,peer,_,keys:=resetStatsFixture(t); a,c:=keys[0],keys[2]
 resetStatsSeed(e,a,1); beforeC:=resetStatsSeed(peer,c,21)
 resetStatsExec(t,s,fmt.Sprintf("SELECT pg_stat_reset_subscription_stats(%d::oid)",a.Object.Address.ObjectID))
 beforeA:=e.Statistics.Read(a)
 var retained statistics.Reader; retained.Configure("snapshot",1); retained.Read(&e.Statistics,a,time.Now())
 var dump bytes.Buffer; if err:=engine.Dump(context.Background(),e,&dump); err!=nil { t.Fatal(err) }
 original:=e.CatalogRead().State()
 if err:=engine.Restore(context.Background(),e,strings.NewReader("CREATE TABLE broken(")); err==nil { t.Fatal("failed restore succeeded") }
 ctx,cancel:=context.WithCancel(context.Background()); cancel()
 if err:=engine.Restore(ctx,e,bytes.NewReader(dump.Bytes())); !errors.Is(err,context.Canceled) { t.Fatal(err) }
 if e.CatalogRead().State()!=original || e.Statistics.Read(a)!=beforeA || peer.Statistics.Read(c)!=beforeC { t.Fatal("failed replacement changed authority") }
 if err:=engine.Restore(context.Background(),e,bytes.NewReader(dump.Bytes())); err!=nil { t.Fatal(err) }
 fresh:=resetStatsKey(t,e,s,"reset_target_a")
 if fresh==a || fresh.Object.Epoch==a.Object.Epoch { t.Fatal("restore retained old incarnation") }
 var reader statistics.Reader; reader.Configure("snapshot",1)
 if got:=reader.Read(&e.Statistics,fresh,time.Now()); got!=(statistics.Fact{Generation:got.Generation}) || !statsLifetimeSnapshotContains(&reader,fresh) { t.Fatal("restored actual subscription baseline",got) }
 resetStatsAbsent(t,e,a)
 if retained.Read(&e.Statistics,a,time.Now())!=beforeA || peer.Statistics.Read(c)!=beforeC { t.Fatal("replacement changed retained capture/peer") }
 projected:=fresh; projected.Object=e.Catalog.Domain.Ref(fresh.Object.Epoch,c.Object.Address); resetStatsAbsent(t,e,projected)
 resetStatsSeed(e,fresh,41)
 if err:=engine.Reset(context.Background(),e); err!=nil { t.Fatal(err) }; resetStatsAbsent(t,e,fresh)
 if peer.Statistics.Read(c)!=beforeC { t.Fatal("selected reset changed peer") }
}
