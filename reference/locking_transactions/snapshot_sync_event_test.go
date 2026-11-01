package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncUtilityEvents(t *testing.T) {
	for _,phase:=range []string{"ddl_command_start","ddl_command_end"}{
		for _,noData:=range []bool{false,true}{
			for _,held:=range []bool{false,true}{
				name:=phase+map[bool]string{false:"/data",true:"/no-data"}[noData]+map[bool]string{false:"/direct",true:"/held"}[held]
				t.Run(name,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
					a,holder,c,observer:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
					// Database-global hooks must retire before fixture schema teardown,
					// including assertions that abort an active exporter/importer.
					t.Cleanup(func(){ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();a.ExecContext(ctx,"ROLLBACK");c.ExecContext(ctx,"ROLLBACK");if _,err:=holder.ExecContext(ctx,"DROP EVENT TRIGGER IF EXISTS sync_event_export");err!=nil{t.Error(err)}})
					syncSeed(t,p,holder)
					fixture.LockExec(t,p.Ctx,holder,"CREATE MATERIALIZED VIEW sync_event_refresh AS SELECT 1 AS id WITH NO DATA",0)
					fixture.LockExec(t,p.Ctx,holder,"CREATE FUNCTION sync_event_export() RETURNS event_trigger LANGUAGE plpgsql STABLE AS $$BEGIN PERFORM pg_advisory_xact_lock(4294967298::bigint);PERFORM set_config('sync.token',pg_export_snapshot(),false);END$$",0)
					fixture.LockExec(t,p.Ctx,holder,"CREATE EVENT TRIGGER sync_event_export ON "+phase+" WHEN TAG IN ('REFRESH MATERIALIZED VIEW') EXECUTE FUNCTION sync_event_export()",0)
					command:="REFRESH MATERIALIZED VIEW sync_event_refresh"
					if noData{command+=" WITH NO DATA"}
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
				})})
			}
		}
	}
}
