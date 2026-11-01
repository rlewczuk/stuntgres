package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncPolicyDemand(t *testing.T) {
	for _,held:=range []bool{false,true}{t.Run(map[bool]string{false:"direct",true:"held"}[held],func(t *testing.T){
		fixture.RoleReferenceCleanup(t,[]string{"snapshot_sync_reader"})
		snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			a,w,c,observer:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
			syncSeed(t,p,w);fixture.LockExec(t,p.Ctx,w,"CREATE TABLE sync_e(id INT,v INT)",0)
			for _,q:=range []string{
				"CREATE ROLE snapshot_sync_reader",
				"GRANT USAGE ON SCHEMA public TO snapshot_sync_reader",
				"CREATE TABLE sync_rls(id INT)",
				"INSERT INTO sync_rls VALUES(1)",
				"ALTER TABLE sync_rls ENABLE ROW LEVEL SECURITY",
				"CREATE POLICY sync_export_policy ON sync_rls USING (CASE WHEN pg_advisory_xact_lock(4294967298::bigint) IS NULL THEN set_config('sync.token',pg_export_snapshot(),false) IS NOT NULL ELSE set_config('sync.token',pg_export_snapshot(),false) IS NOT NULL END)",
				"GRANT SELECT ON sync_rls TO snapshot_sync_reader",
			}{rows,err:=w.QueryContext(p.Ctx,q);if err!=nil{t.Fatal(q,err)};for rows.Next(){};if err:=rows.Close();err!=nil{t.Fatal(err)}}
			ap,wp:=fixture.LockPID(t,p.Ctx,a),fixture.LockPID(t,p.Ctx,w)
			fixture.LockExec(t,p.Ctx,a,"SET ROLE snapshot_sync_reader",0)
			const query="SELECT id AS v FROM sync_rls ORDER BY id"
			var statement *sql.Stmt;var err error
			if held{statement,err=a.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer statement.Close()}
			fixture.LockExec(t,p.Ctx,w,"SELECT pg_advisory_lock(4294967298::bigint)",1)
			fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
			join:=fixture.LockOperation(t,p.Ctx,func(ctx context.Context)fixture.RelationOutcome{var rows *sql.Rows;var err error;if held{rows,err=statement.QueryContext(ctx)}else{rows,err=a.QueryContext(ctx,query)};return fixture.RelationOutcome{Rows:rows,Err:err}})
			advisoryXactWait(t,p,observer,ap,wp,advisorySessionKeys[0],false)
			fixture.LockExec(t,p.Ctx,w,"UPDATE sync_t SET v=20 WHERE id=1",1);fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_e VALUES(2,20)",1)
			fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT pg_advisory_unlock(4294967298::bigint) AS unlocked",[]string{"unlocked"},[]string{"BOOL"},[][]any{{true}}))
			out:=join();fixture.LimitLockValues(t,out,b.PG,1)
			var token string;if err:=a.QueryRowContext(p.Ctx,"SELECT current_setting('sync.token')").Scan(&token);err!=nil{t.Fatal(err)}
			fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
			syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}});syncRows(t,p,b,c,"sync_e",nil)
		})
	})}
}
