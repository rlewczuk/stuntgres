package core_test

import (
 "context"
 "errors"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/subscription"
 "stuntgres/internal/parser"
)

func originStage(t *testing.T,e *core.Engine,s *session.Session,name string) (*core.StatementScope,*core.CatalogStage,*catalog.Subscription,func()) {
 t.Helper()
 op,err:=s.Acquire(context.Background());if err!=nil { t.Fatal(err) }
 scope,err:=e.StartStatement(op);if err!=nil { op.Done();t.Fatal(err) }
 command:=enginetest.Statement(t,"CREATE SUBSCRIPTION "+name+" CONNECTION '' PUBLICATION remote WITH (connect=false,slot_name=NONE)").(*parser.Subscription)
 if err:=scope.Gate(command);err!=nil { scope.Finish(err);op.Done();t.Fatal(err) }
 e.Mu.Lock()
 auth,err:=e.AuthorizationLocked(op)
 if err!=nil { e.Mu.Unlock();scope.Finish(err);op.Done();t.Fatal(err) }
 stage:=e.BeginCatalogStage(auth.Catalog);stage.AuthorizeLocked(auth,s)
 err=subscription.Stage(core.DDLHost{E:e,Op:op,CatalogStage:stage},auth,command)
 e.Mu.Unlock()
 if err!=nil { e.DiscardCatalogStage(stage);scope.Finish(err);op.Done();t.Fatal(err) }
 var descriptor *catalog.Subscription
 for _,d:=range stage.StageRead().Subscriptions() { if d.Name()==name { descriptor=d } }
 if descriptor==nil { t.Fatal("stage did not create actual subscription descriptor") }
 cleanup:=func(){e.DiscardCatalogStage(stage);scope.Finish(context.Canceled);op.Done()}
 t.Cleanup(cleanup)
 return scope,stage,descriptor,cleanup
}
func transferOriginStage(t *testing.T,e *core.Engine,scope *core.StatementScope,stage *core.CatalogStage) {
 t.Helper()
 if _,err:=e.ValidateCatalogStage(scope.Op.Context(),stage);err!=nil { t.Fatal(err) }
 if err:=e.StageCatalogOverlay(scope,stage,scope.PendingCatalog().Clone());err!=nil { t.Fatal(err) }
 core.SetScopeWorked(scope,true)
}
func TestReplicationOriginStageOwnership(t *testing.T) {
 e:=engine.New()
 a:=enginetest.StartSession(t,e,nil);b:=enginetest.StartSession(t,e,nil)
 ref:=enginetest.TransactionBegin(t,a)
 first,stage,d,finish:=originStage(t,e,a,"origin_owned_stage")
 // The provisional stage excludes its real ID before any journal owns it.
 probe,probeStage,other,probeDone:=originStage(t,e,b,"origin_owned_probe")
 if d.ReplicationOriginID==0 || other.ReplicationOriginID==d.ReplicationOriginID { t.Fatal("provisional reservation collision") }
 e.DiscardCatalogStage(probeStage);probe.Finish(context.Canceled);probeDone()
 transferOriginStage(t,e,first,stage)
 if a.Transactions.Catalog!=nil || first.Catalog==nil { t.Fatal("reservation did not survive Execute-to-Finish ownership interval") }
 retained:=first.Catalog
 if err:=first.Finish(nil);err!=nil { t.Fatal(err) };finish()
 if a.Transactions.Catalog!=retained || retained.Descriptors[d.Address()]!=d { t.Fatal("journal transfer lost actual descriptor") }
 a.Transactions.Mu.Lock();mark:=core.CaptureSavepoint(a.Transactions,"retained");a.Transactions.Savepoints=append(a.Transactions.Savepoints,mark);a.Transactions.Mu.Unlock()
 // Restore the real captured ancestor, then terminal rollback releases it.
 a.Transactions.Mu.Lock();core.RestoreSavepoint(a.Transactions,mark,mark.Settings.Clone());a.Transactions.Mu.Unlock()
 if a.Transactions.Catalog!=retained { t.Fatal("savepoint failed to restore journal ownership") }
 enginetest.TransactionEnd(t,a,ref,false)
 next,nextStage,reused,nextDone:=originStage(t,e,b,"origin_after_rollback")
 if reused.ReplicationOriginID!=d.ReplicationOriginID { t.Fatal("rollback leaked lowest reservation",reused.ReplicationOriginID,d.ReplicationOriginID) }
 e.DiscardCatalogStage(nextStage);next.Finish(context.Canceled);nextDone()
 // A real failed Finish after transfer must not retain the statement suffix.
 ref=enginetest.TransactionBegin(t,a)
 failed,failedStage,bad,failedDone:=originStage(t,e,a,"origin_failed_finish")
 transferOriginStage(t,e,failed,failedStage)
 failure:=errors.New("owner publication veto")
 if err:=failed.Finish(failure);!errors.Is(err,failure) { t.Fatal(err) };failedDone()
 enginetest.TransactionEnd(t,a,ref,false)
 last,lastStage,available,lastDone:=originStage(t,e,b,"origin_after_failure")
 if available.ReplicationOriginID!=bad.ReplicationOriginID { t.Fatal("failed Finish leaked reservation") }
 e.DiscardCatalogStage(lastStage);last.Finish(context.Canceled);lastDone()
	enginetest.TransactionBegin(t,a)
	ending, endingStage, terminal, endingDone := originStage(t,e,a,"origin_session_end")
	transferOriginStage(t,e,ending,endingStage)
	if err := ending.Finish(nil); err != nil { t.Fatal(err) }
	endingDone()
	a.RequestEnd(session.CleanClose)
	if err := a.WaitEnd(context.Background()); err != nil { t.Fatal(err) }
	probeEnd, stageEnd, freed, endDone := originStage(t,e,b,"origin_after_session")
	if freed.ReplicationOriginID != terminal.ReplicationOriginID { t.Fatal("physical session termination leaked reservation") }
	e.DiscardCatalogStage(stageEnd); probeEnd.Finish(context.Canceled); endDone()
	c := enginetest.StartSession(t,e,nil)
	invalidScope, invalidStage, invalid, invalidDone := originStage(t,e,c,"origin_invalid_candidate")
	invalid.ObjectRevision = 0
	if _,err := e.ValidateCatalogStage(context.Background(),invalidStage); err == nil { t.Fatal("invalid descriptor stage admitted") }
	e.DiscardCatalogStage(invalidStage); invalidScope.Finish(context.Canceled); invalidDone()
	afterInvalid, afterStage, reclaimed, afterDone := originStage(t,e,b,"origin_after_validation_failure")
	if reclaimed.ReplicationOriginID != invalid.ReplicationOriginID { t.Fatal("failed validation leaked provisional ownership") }
	e.DiscardCatalogStage(afterStage); afterInvalid.Finish(context.Canceled); afterDone()
	commitSession := enginetest.StartSession(t,e,nil)
	commitRef := enginetest.TransactionBegin(t,commitSession)
	commitScope, commitStage, committedCandidate, commitDone := originStage(t,e,commitSession,"origin_failed_commit")
	transferOriginStage(t,e,commitScope,commitStage)
	if err := commitScope.Finish(nil); err != nil { t.Fatal(err) }
	commitDone()
	// Corrupt only the private successor, retaining the actual valid journal as
	// evidence that failed final validation must dispose this incarnation.
	owner := commitSession.Transactions
	owner.Admission.Lock()
	owner.Mu.Lock()
	ancestor := owner.Catalog
	owner.Catalog = ancestor.Clone()
	forgedCommit := *committedCandidate
	forgedCommit.ObjectRevision = 0
	owner.Catalog.Descriptors[forgedCommit.Address()] = &forgedCommit
	owner.Mu.Unlock()
	owner.Admission.Unlock()
	beforeCommit := e.CatalogRead().State()
	commitOp, err := commitSession.Acquire(context.Background())
	if err != nil { t.Fatal(err) }
	_, err = e.Commit(commitOp,commitRef)
	commitOp.Done()
	if err == nil || e.CatalogRead().State() != beforeCommit || ancestor.Descriptors[committedCandidate.Address()] != committedCandidate {
		t.Fatal("failed commit changed registered roots or retained journal",err)
	}
	afterCommit, afterCommitStage, commitFreed, afterCommitDone := originStage(t,e,b,"origin_after_failed_commit")
	if commitFreed.ReplicationOriginID != committedCandidate.ReplicationOriginID { t.Fatal("failed commit leaked reservation") }
	e.DiscardCatalogStage(afterCommitStage); afterCommit.Finish(context.Canceled); afterCommitDone()
}

// Exercise generic descriptor removal, without a subscription parser cleanup.
func originRemoveStage(t *testing.T, e *core.Engine, scope *core.StatementScope, name string) {
	t.Helper()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil { e.Mu.Unlock(); t.Fatal(err) }
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	address := auth.Catalog.NameAddress(catalog.SubscriptionNameKey(e.Catalog.DatabaseAddress(),name))
	previous, ok := auth.Catalog.Get(address).(*catalog.Subscription)
	if !ok { e.Mu.Unlock(); t.Fatal("generic removal lacks actual subscription predecessor") }
	err = stage.RemoveObjectLocked(auth.Ref(address),false)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil { t.Fatal(err) }
	transferOriginStage(t, e, scope, stage)
	e.Mu.Lock()
	private := e.PrivateCatalogLocked(scope.Catalog)
	for _, d := range private.Subscriptions() {
		if d.Address() == address || d.ReplicationOriginID == previous.ReplicationOriginID {
			e.Mu.Unlock()
			t.Fatal("generic descriptor removal retained origin registration")
		}
	}
	e.Mu.Unlock()
}

func TestReplicationOriginExceptionOwnership(t *testing.T) {
	e := engine.New()
	a, b := enginetest.StartSession(t,e,nil), enginetest.StartSession(t,e,nil)
	ref := enginetest.TransactionBegin(t,a)
	scope, stage, d, done := originStage(t,e,a,"origin_exception_owned")
	transferOriginStage(t,e,scope,stage)
	actual := scope.Catalog
	if a.Transactions.Catalog != nil { t.Fatal("fixture registration must live only in the statement journal") }
	outer, err := scope.BeginPLException()
	if err != nil { t.Fatal(err) }
	if len(a.Transactions.Savepoints) != 1 || a.Transactions.Savepoints[0].StatementCatalog != actual {
		t.Fatal("exception did not retain actual statement backing")
	}
	originRemoveStage(t,e,scope,d.Name())
	inner, err := scope.BeginPLException()
	if err != nil { t.Fatal(err) }
	if err := scope.EndPLException(inner,false,nil); err != nil { t.Fatal(err) }
	probe, probeStage, reserved, probeDone := originStage(t,e,b,"origin_exception_probe")
	if reserved.ReplicationOriginID == d.ReplicationOriginID { t.Fatal("nested release freed a revivable statement-only identity") }
	e.DiscardCatalogStage(probeStage); probe.Finish(context.Canceled); probeDone()
	if err := scope.EndPLException(outer,true,errors.New("restore outer")); err != nil { t.Fatal(err) }
	if scope.Catalog != actual || scope.Catalog.Descriptors[d.Address()] != d || len(a.Transactions.Savepoints) != 0 {
		t.Fatal("outer recovery did not restore exact immutable statement journal")
	}
	originRemoveStage(t,e,scope,d.Name())
	if err := scope.Finish(nil); err != nil { t.Fatal(err) }
	done()
	next, nextStage, available, nextDone := originStage(t,e,b,"origin_exception_reused")
	if available.ReplicationOriginID != d.ReplicationOriginID { t.Fatal("consumed exception mark retained unreachable allocation") }
	e.DiscardCatalogStage(nextStage); next.Finish(context.Canceled); nextDone()
	enginetest.TransactionEnd(t,a,ref,false)
}
