package reference_test

import (
	"context"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncCancellation(t *testing.T){
	snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,holder,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
		pid:=fixture.LockPID(t,p.Ctx,a)
		fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_gate(id INT)",0)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0);token:=syncExport(t,p,b,a,nil)
		fixture.LockExec(t,p.Ctx,holder,"BEGIN",0);fixture.LockExec(t,p.Ctx,holder,"LOCK TABLE sync_gate IN ACCESS EXCLUSIVE MODE",0)
		const query="SELECT pg_export_snapshot() FROM sync_gate"
		fixture.RelationLockActiveCancel(t,p,b,a,query,func(ctx context.Context)fixture.RelationOutcome{rows,err:=a.QueryContext(ctx,query);return fixture.RelationOutcome{Rows:rows,Err:err}})
		syncAborted(t,p,c,token,pid)
		fixture.LockExec(t,p.Ctx,a,"ROLLBACK",0);syncExpired(t,p,c,token)
		fixture.LockExec(t,p.Ctx,holder,"ROLLBACK",0)
	})
}
