package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncUtilitySnapshots(t *testing.T) {
	for _, held := range []bool{false,true} {
		t.Run("ctas-"+map[bool]string{false:"direct",true:"held"}[held],func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			a,holder,w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
			syncSeed(t,p,w)
			fixture.LockExec(t,p.Ctx,w,"CREATE TABLE sync_gate(id INT)",0)
			fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_gate VALUES(1)",1)
			waiterPID,holderPID:=fixture.LockPID(t,p.Ctx,a),fixture.LockPID(t,p.Ctx,holder)
			var oid int64;if err:=holder.QueryRowContext(p.Ctx,"SELECT 'sync_gate'::regclass::oid::bigint").Scan(&oid);err!=nil{t.Fatal(err)}
			const command="CREATE TABLE sync_utility_cut AS SELECT pg_export_snapshot() AS token FROM sync_gate"
			var statement *sql.Stmt
			if held{var err error;statement,err=a.PrepareContext(p.Ctx,command);if err!=nil{t.Fatal(err)};defer statement.Close()}
			fixture.LockExec(t,p.Ctx,holder,"BEGIN",0);fixture.LockExec(t,p.Ctx,holder,"LOCK TABLE sync_gate IN ACCESS EXCLUSIVE MODE",0)
			fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
			done:=make(chan struct{})
			join:=fixture.LockOperation(t,p.Ctx,func(ctx context.Context)fixture.RelationOutcome{
				defer close(done)
				var result sql.Result;var err error
				if held{result,err=statement.ExecContext(ctx)}else{result,err=a.ExecContext(ctx,command)}
				return fixture.RelationOutcome{Result:result,Err:err}
			})
			if b.PG{fixture.LockWaitBehind(t,p.Ctx,waiterPID,holderPID,oid,"AccessShareLock")}else{fixture.NativeRelationLockWait(t,p.Ctx,done)}
			fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,20)",1)
			fixture.LockExec(t,p.Ctx,holder,"COMMIT",0)
			out:=join();if out.Err!=nil{t.Fatal(out.Err)};fixture.LockAffected(t,out.Result,1)
			var token string;if err:=a.QueryRowContext(p.Ctx,"SELECT token FROM sync_utility_cut").Scan(&token);err!=nil||token==""{t.Fatal(token,err)}
			fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
			// Analysis acquires the CTAS source lock before its utility pair;
			// the publication made while that source grant waited is included.
			syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(2),int64(20)},{int64(3),int64(30)}})
			fixture.LockExec(t,p.Ctx,a,"COMMIT",0)
			syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(2),int64(20)},{int64(3),int64(30)}})
			fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0);syncExpired(t,p,c,token)
		})})
		t.Run(map[bool]string{false:"direct",true:"held"}[held],func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			a,holder,c,observer:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
			syncSeed(t,p,holder)
			fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_e(id INT,v INT)",0)
			fixture.LockExec(t,p.Ctx,holder,"CREATE VIEW sync_refresh_source AS SELECT CASE WHEN pg_advisory_xact_lock(4294967298::bigint) IS NULL THEN pg_export_snapshot() ELSE pg_export_snapshot() END AS token",0)
			fixture.LockExec(t,p.Ctx,holder,"CREATE MATERIALIZED VIEW sync_refresh AS SELECT token FROM sync_refresh_source WITH NO DATA",0)
			const command="REFRESH MATERIALIZED VIEW sync_refresh"
			var statement *sql.Stmt
			if held{var err error;statement,err=a.PrepareContext(p.Ctx,command);if err!=nil{t.Fatal(err)};defer statement.Close()}
			waiterPID,holderPID:=fixture.LockPID(t,p.Ctx,a),fixture.LockPID(t,p.Ctx,holder)
			fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
			fixture.LockExec(t,p.Ctx,holder,"SELECT pg_advisory_lock(4294967298::bigint)",1)
			join:=fixture.LockOperation(t,p.Ctx,func(ctx context.Context)fixture.RelationOutcome{
				var result sql.Result;var err error
				if held{result,err=statement.ExecContext(ctx)}else{result,err=a.ExecContext(ctx,command)}
				return fixture.RelationOutcome{Result:result,Err:err}
			})
			advisoryXactWait(t,p,observer,waiterPID,holderPID,advisorySessionKeys[0],false)
			fixture.LockExec(t,p.Ctx,holder,"UPDATE sync_t SET v=20 WHERE id=1",1)
			fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_e VALUES(2,20)",1)
			fixture.RunStep(t,p.Ctx,holder,b.Recorder,fixture.Select("SELECT pg_advisory_unlock(4294967298::bigint) AS unlocked",[]string{"unlocked"},[]string{"BOOL"},[][]any{{true}}))
			out:=join();if out.Err!=nil{t.Fatal(out.Err)};fixture.LockAffected(t,out.Result,0)
			var token string;if err:=a.QueryRowContext(p.Ctx,"SELECT token FROM sync_refresh").Scan(&token);err!=nil||token==""{t.Fatal(token,err)}
			fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
			syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}});syncRows(t,p,b,c,"sync_e",nil)
			fixture.LockExec(t,p.Ctx,a,"COMMIT",0)
			syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
			fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0);syncExpired(t,p,c,token)
		})})
	}
}
