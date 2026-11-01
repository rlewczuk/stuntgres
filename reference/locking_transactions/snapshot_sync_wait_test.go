package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncGrantWait(t *testing.T){
	for _,held:=range []bool{false,true}{t.Run(map[bool]string{false:"direct",true:"held"}[held],func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,holder,w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
		fixture.LockExec(t,p.Ctx,w,"CREATE TABLE sync_gate(id INT)",0);fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_gate VALUES(1)",1)
		syncSeed(t,p,w)
		waiterPID,holderPID:=fixture.LockPID(t,p.Ctx,a),fixture.LockPID(t,p.Ctx,holder)
		var oid int64;if err:=holder.QueryRowContext(p.Ctx,"SELECT 'sync_gate'::regclass::oid::bigint").Scan(&oid);err!=nil{t.Fatal(err)}
		const query="SELECT pg_export_snapshot() FROM sync_gate"
		var statement *sql.Stmt;var err error
		if held{statement,err=a.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer statement.Close()}
		fixture.LockExec(t,p.Ctx,holder,"BEGIN",0);fixture.LockExec(t,p.Ctx,holder,"LOCK TABLE sync_gate IN ACCESS EXCLUSIVE MODE",0)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
		done:=make(chan struct{})
		join:=fixture.LockOperation(t,p.Ctx,func(ctx context.Context)fixture.RelationOutcome{
			defer close(done)
			var rows *sql.Rows;var err error
			if held{rows,err=statement.QueryContext(ctx)}else{rows,err=a.QueryContext(ctx,query)}
			return fixture.RelationOutcome{Rows:rows,Err:err}
		})
		if b.PG{fixture.LockWaitBehind(t,p.Ctx,waiterPID,holderPID,oid,"AccessShareLock")}else{fixture.NativeRelationLockWait(t,p.Ctx,done)}
		// Publication happens after an observed queued grant, not after a sleep
		// or a worker-launch signal. RC must include it in the exported cut.
		fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,20)",1)
		fixture.LockExec(t,p.Ctx,holder,"COMMIT",0)
		out:=join();if out.Err!=nil{t.Fatal(out.Err)};token:=syncExportRows(t,b,out.Rows)
		fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
		syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(2),int64(20)},{int64(3),int64(30)}})
	})})}
}
