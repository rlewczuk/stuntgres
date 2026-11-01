package reference_test

import (
	"testing"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncDatabase(t *testing.T) {
	databases:=[]string{"snapshot_sync_other"};fixture.DatabaseReferenceCleanup(t,&databases)
	snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a:=snapshotConn(t,p,b);fixture.LockExec(t,p.Ctx,a,"CREATE DATABASE snapshot_sync_other",0)
		pool,_,recorder:=fixture.DatabaseScenarioPool(t,p,b.PG,"snapshot_sync_other","")
		other:=b;other.DB,other.Recorder=pool,recorder
		x,y:=snapshotConn(t,p,other),snapshotConn(t,p,other)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0);token:=syncExport(t,p,b,a,nil)
		fixture.LockExec(t,p.Ctx,x,"BEGIN ISOLATION LEVEL REPEATABLE READ",0)
		syncError(t,p,x,"SET TRANSACTION SNAPSHOT "+snapshotQuote(token),"0A000","cannot import a snapshot from a different database")
		fixture.LockExec(t,p.Ctx,x,"ROLLBACK",0)
		fixture.LockExec(t,p.Ctx,x,"BEGIN",0);otherToken:=syncExport(t,p,other,x,nil)
		if token==otherToken{t.Fatal("cluster reused snapshot identity across databases")}
		if !b.PG {if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}}
		fixture.LockExec(t,p.Ctx,y,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,y,otherToken)
		fixture.RunStep(t,p.Ctx,y,other.Recorder,fixture.Select("SELECT 1 AS healthy",[]string{"healthy"},[]string{"INT4"},[][]any{{int64(1)}}))
		fixture.LockExec(t,p.Ctx,y,"ROLLBACK",0);fixture.LockExec(t,p.Ctx,x,"ROLLBACK",0);syncExpired(t,p,y,otherToken)
	})
}
