package reference_test

import (
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncAdvertisement(t *testing.T) {
	snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,w,builder:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
		ap,bp:=fixture.ConcurrentIndexPID(t,p,b,a),fixture.ConcurrentIndexPID(t,p,b,builder)
		fixture.LockExec(t,p.Ctx,w,"CREATE TABLE sync_index_target(id INT)",0)
		fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_index_target VALUES(1)",1)
		fixture.LockExec(t,p.Ctx,w,"CREATE TABLE sync_index_unread(id INT)",0)
		fixture.LockExec(t,p.Ctx,a,"BEGIN ISOLATION LEVEL READ COMMITTED",0)
		old:=syncExport(t,p,b,a,nil)
		build:=fixture.ConcurrentIndexStart(t,p,builder,"CREATE INDEX CONCURRENTLY sync_retained_ix ON sync_index_target(id)",false)
		fixture.ConcurrentIndexWait(t,p,b,"sync_retained_ix",stuntgres.IndexState{Ready:true,Live:true},bp,ap,"waiting for old snapshots")
		fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_index_unread VALUES(2)",1)
		newer:=syncExport(t,p,b,a,nil)
		if newer==old{t.Fatal("newer cut reused export identifier")}
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT 1 AS healthy",[]string{"healthy"},[]string{"INT4"},[][]any{{int64(1)}}))
		// The first exported cut remains a real concurrent-index blocker even
		// after a newer RC export and a subsequent source command have ended.
		fixture.ConcurrentIndexWait(t,p,b,"sync_retained_ix",stuntgres.IndexState{Ready:true,Live:true},bp,ap,"waiting for old snapshots")
		fixture.LockExec(t,p.Ctx,a,"COMMIT",0)
		if err:=build.Join(t,p);err!=nil{t.Fatal(err)}
		fixture.ConcurrentIndexWait(t,p,b,"sync_retained_ix",stuntgres.IndexState{Ready:true,Valid:true,Live:true},0,0,"")
		syncExpired(t,p,w,old);syncExpired(t,p,w,newer)
	})
}
