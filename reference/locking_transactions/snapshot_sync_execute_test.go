package reference_test

import (
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotSyncExecuteActuals(t *testing.T) {
	for _,actual := range []string{"pg_export_snapshot()", "sync_actual_export()", "CAST(pg_export_snapshot() AS text)"} {
		t.Run(actual,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			a,w,c:=snapshotConn(t,p,b),snapshotConn(t,p,b),snapshotConn(t,p,b)
			syncSeed(t,p,w)
			fixture.LockExec(t,p.Ctx,a,"CREATE FUNCTION sync_actual_export() RETURNS TEXT LANGUAGE SQL STABLE AS $$SELECT pg_export_snapshot()$$",0)
			fixture.LockExec(t,p.Ctx,a,"PREPARE sync_actual(text) AS SELECT $1 AS token",0)
			fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
			var token string
			if err:=a.QueryRowContext(p.Ctx,"EXECUTE sync_actual("+actual+")").Scan(&token);err!=nil||token==""{t.Fatal("actual export",token,err)}
			fixture.LockExec(t,p.Ctx,w,"INSERT INTO sync_t VALUES(2,20)",1)
			fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0);syncImport(t,p,c,token)
			syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
			fixture.LockExec(t,p.Ctx,a,"COMMIT",0);syncRows(t,p,b,c,"sync_t",[][]any{{int64(1),int64(10)},{int64(3),int64(30)}})
			syncExpired(t,p,w,token)
		})})
	}
	t.Run("diagnostics-before-actual-runtime",func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		a,w:=snapshotConn(t,p,b),snapshotConn(t,p,b)
		fixture.LockExec(t,p.Ctx,a,"PREPARE sync_actual(integer,text) AS SELECT $2",0)
		fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
		for _,query:=range []string{"EXECUTE sync_actual(pg_export_snapshot())","EXECUTE sync_actual(TRUE,pg_export_snapshot())"}{
			fixture.LockExec(t,p.Ctx,a,"SAVEPOINT actual_error",0)
			want:=fixture.ControlsReferenceDiagnostic("42601",`wrong number of parameters for prepared statement "sync_actual"`)
			want.Detail="Expected 2 parameters but got 1."
			if strings.Contains(query,"TRUE"){
				want=fixture.ControlsReferenceDiagnostic("42804","parameter $1 of type boolean cannot be coerced to the expected type integer")
				want.Hint="You will need to rewrite or cast the expression."
				want.Position=strconv.Itoa(strings.Index(query,"TRUE")+1)
			}
			_,err:=a.ExecContext(p.Ctx,query);fixture.AssertDiagnostic(t,err,&want)
			fixture.LockExec(t,p.Ctx,a,"ROLLBACK TO actual_error",0);fixture.LockExec(t,p.Ctx,a,"RELEASE actual_error",0)
		}
		fixture.PreparedReferenceCleanup(t,b,"sync_actual_noexport")
		fixture.LockExec(t,p.Ctx,a,"PREPARE TRANSACTION 'sync_actual_noexport'",0)
		fixture.PreparedReferenceOne(t,p,w,"sync_actual_noexport")
		fixture.LockExec(t,p.Ctx,w,"ROLLBACK PREPARED 'sync_actual_noexport'",0)
	})})
}
