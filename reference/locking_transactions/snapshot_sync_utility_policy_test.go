package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncPartitionUtility(t *testing.T) {
	for _,held:=range []bool{false,true}{t.Run(map[bool]string{false:"direct",true:"held"}[held],func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,holder,c,observer:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
		syncSeed(t,p,holder)
		fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_partitioned(k TEXT) PARTITION BY LIST(k)",0)
		const command="CREATE TABLE sync_partition PARTITION OF sync_partitioned FOR VALUES IN(CASE WHEN pg_advisory_xact_lock(4294967298::bigint) IS NULL THEN set_config('sync.token',pg_export_snapshot(),false) ELSE set_config('sync.token',pg_export_snapshot(),false) END)"
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
		fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_t VALUES(2,20)",1)
		fixture.RunStep(t,p.Ctx,holder,b.Recorder,fixture.Select("SELECT pg_advisory_unlock(4294967298::bigint) AS unlocked",[]string{"unlocked"},[]string{"BOOL"},[][]any{{true}}))
		out:=join();if out.Err!=nil{t.Fatal(out.Err)};fixture.LockAffected(t,out.Result,0)
		var token string;if err:=a.QueryRowContext(p.Ctx,"SELECT current_setting('sync.token')").Scan(&token);err!=nil||token==""{t.Fatal(token,err)}
		fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
		syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
		fixture.LockExec(t,p.Ctx,a,"COMMIT",0)
		syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
		fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0);syncExpired(t,p,c,token)
	})})}
}

func TestReferenceSnapshotSyncUtilityPolicySelection(t *testing.T) {
	snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a:=snapshotConn(t,p,b)
		fixture.LockExec(t,p.Ctx,a,"CREATE TABLE sync_policy_source(id INT)",0)
		fixture.LockExec(t,p.Ctx,a,"INSERT INTO sync_policy_source VALUES(1),(3)",2)
		fixture.LockExec(t,p.Ctx,a,"ALTER TABLE sync_policy_source ENABLE ROW LEVEL SECURITY",0)
		fixture.LockExec(t,p.Ctx,a,"CREATE POLICY sync_policy_export ON sync_policy_source USING(set_config('sync.token',pg_export_snapshot(),false) IS NULL)",0)
		fixture.LockExec(t,p.Ctx,a,"CREATE MATERIALIZED VIEW sync_policy_refresh AS SELECT id FROM sync_policy_source WITH NO DATA",0)
		fixture.LockExec(t,p.Ctx,a,"SET sync.token='pristine'",0)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
		fixture.LockExec(t,p.Ctx,a,"REFRESH MATERIALIZED VIEW sync_policy_refresh",0)
		// The fixture owner bypasses RLS. Pure possible-policy demand must not
		// execute the policy, change settings, filter rows, or publish a token.
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT current_setting('sync.token') AS token",[]string{"token"},[]string{"TEXT"},[][]any{{"pristine"}}))
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT id FROM sync_policy_refresh ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(3)}}))
		fixture.PreparedReferenceCleanup(t,b,"sync_policy_noexport")
		fixture.LockExec(t,p.Ctx,a,"PREPARE TRANSACTION 'sync_policy_noexport'",0)
		fixture.PreparedReferenceOne(t,p,a,"sync_policy_noexport")
		fixture.LockExec(t,p.Ctx,a,"ROLLBACK PREPARED 'sync_policy_noexport'",0)
	})
}
