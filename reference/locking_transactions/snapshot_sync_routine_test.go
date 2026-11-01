package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncRoutineSnapshots(t *testing.T) {
	for _,isolation:=range []string{"READ COMMITTED","REPEATABLE READ","SERIALIZABLE"}{
		for _,volatility:=range []string{"STABLE","VOLATILE"}{
			for _,held:=range []bool{false,true}{
				name:=isolation+"/"+volatility;if held{name+="/held"}
				t.Run(name,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
					a,w,c,observer:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
					syncSeed(t,p,w)
					// Multiple commands prevent SQL inlining from replacing the
					// real routine active-snapshot boundary with an outer call.
					fixture.LockExec(t,p.Ctx,w,"CREATE FUNCTION sync_nested_export() RETURNS TEXT LANGUAGE SQL "+volatility+" AS $$SELECT 1;SELECT pg_export_snapshot()$$",0)
					ap,wp:=fixture.LockPID(t,p.Ctx,a),fixture.LockPID(t,p.Ctx,w)
					const query="SELECT pg_export_snapshot() AS outer_token,CASE WHEN pg_advisory_xact_lock(4294967298::bigint) IS NULL THEN sync_nested_export() ELSE sync_nested_export() END AS child_token,pg_export_snapshot() AS resumed_token"
					var statement *sql.Stmt;var err error
					if held{statement,err=a.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer statement.Close()}
					fixture.LockExec(t,p.Ctx,w,"SELECT pg_advisory_lock(4294967298::bigint)",1)
					fixture.LockExec(t,p.Ctx,a,"BEGIN ISOLATION LEVEL "+isolation,0)
					join:=fixture.LockOperation(t,p.Ctx,func(ctx context.Context)fixture.RelationOutcome{var rows *sql.Rows;var err error;if held{rows,err=statement.QueryContext(ctx)}else{rows,err=a.QueryContext(ctx,query)};return fixture.RelationOutcome{Rows:rows,Err:err}})
					advisoryXactWait(t,p,observer,ap,wp,advisorySessionKeys[0],false)
					fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,20)",1)
					fixture.RunStep(t,p.Ctx,w,b.Recorder,fixture.Select("SELECT pg_advisory_unlock(4294967298::bigint) AS unlocked",[]string{"unlocked"},[]string{"BOOL"},[][]any{{true}}))
					out:=join();if out.Err!=nil{t.Fatal(out.Err)};defer out.Rows.Close()
					if !out.Rows.Next(){t.Fatal(out.Rows.Err())};var tokens [3]string
					if err:=out.Rows.Scan(&tokens[0],&tokens[1],&tokens[2]);err!=nil{t.Fatal(err)}
					if out.Rows.Next()||out.Rows.Err()!=nil{t.Fatal("extra routine export rows",out.Rows.Err())};out.Rows.Close()
					if tokens[0]==""||tokens[1]==""||tokens[2]==""||tokens[0]==tokens[1]||tokens[0]==tokens[2]||tokens[1]==tokens[2]{t.Fatal("routine exports reused identity",tokens)}
					for i,token:=range tokens{
						fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
						want:=[][]any{{int64(1),int64(10)},{int64(3),int64(30)}}
						if i==1&&isolation=="READ COMMITTED"&&volatility=="VOLATILE"{want=[][]any{{int64(1),int64(10)},{int64(2),int64(20)},{int64(3),int64(30)}}}
						syncRows(t,p,b,c,"sync_t",want);fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0)
					}
					fixture.LockExec(t,p.Ctx,a,"COMMIT",0)
					for _,token:=range tokens{syncExpired(t,p,c,token)}
				})})
			}
		}
	}
	t.Run("transaction-rotating-call",func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,c:=snapshotConn(t,p,b),snapshotConn(t,p,b);syncSeed(t,p,c)
		fixture.LockExec(t,p.Ctx,a,"CREATE PROCEDURE sync_rotate(INOUT before_token TEXT,INOUT after_token TEXT,INOUT after_count BIGINT) LANGUAGE plpgsql AS $$BEGIN INSERT INTO sync_t VALUES(9,90);before_token:=pg_export_snapshot();COMMIT;after_token:=pg_export_snapshot();SELECT count(*) INTO after_count FROM sync_t;END$$",0)
		var before,after string;var count int64
		if err:=a.QueryRowContext(p.Ctx,"CALL sync_rotate(NULL,NULL,NULL)").Scan(&before,&after,&count);err!=nil{t.Fatal(err)}
		if before==""||after==""||before==after||count!=3{t.Fatal("rotating CALL retained old incarnation/cut",before,after,count)}
		syncExpired(t,p,c,before);syncExpired(t,p,c,after)
		syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)},{int64(9),int64(90)}})
	})})
}
