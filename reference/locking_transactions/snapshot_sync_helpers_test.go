package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Tokens are backend-owned identities. Never compare allocator output between
// servers; independently check the released character and lifetime contracts.
func syncExport(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held *sql.Stmt) string {
	t.Helper()
	var rows *sql.Rows
	var err error
	if held == nil { rows, err = c.QueryContext(p.Ctx, "SELECT pg_export_snapshot()") } else { rows, err = held.QueryContext(p.Ctx) }
	if err != nil { t.Fatal(err) }
	return syncExportRows(t, b, rows)
}

func syncExportRows(t *testing.T, b fixture.Backend, rows *sql.Rows) string {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || len(names) != 1 || names[0] != "pg_export_snapshot" { t.Fatalf("export names %v: %v", names, err) }
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "TEXT" { t.Fatalf("export TEXT metadata %v: %v", types, err) }
	if !b.PG { if nullable, known := types[0].Nullable(); nullable || !known { t.Fatalf("export nullable %v/%v", nullable, known) } }
	if !rows.Next() { t.Fatalf("export missing row: %v", rows.Err()) }
	var token sql.NullString
	if err := rows.Scan(&token); err != nil { t.Fatal(err) }
	if !token.Valid || token.String == "" || strings.IndexFunc(token.String, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'A' && r <= 'F' || r == '-') }) >= 0 { t.Fatalf("invalid export identifier %v", token) }
	if rows.Next() || rows.Err() != nil { t.Fatalf("export extra row/error: %v", rows.Err()) }
	return token.String
}

func syncImport(t *testing.T, p *fixture.Pair, c *sql.Conn, token string) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, c, "SET TRANSACTION SNAPSHOT "+snapshotQuote(token), 0)
}

func syncError(t *testing.T, p *fixture.Pair, c *sql.Conn, query, code, message string) {
	t.Helper()
	fixture.PreparedReferenceError(t, p.Ctx, c, query, code, message)
}

func syncExpired(t *testing.T, p *fixture.Pair, c *sql.Conn, token string) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, c, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
	syncError(t, p, c, "SET TRANSACTION SNAPSHOT "+snapshotQuote(token), "42704", "snapshot \""+token+"\" does not exist")
	fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
}

func syncAborted(t *testing.T, p *fixture.Pair, c *sql.Conn, token string, pid int) {
	t.Helper()
	fixture.LockExec(t,p.Ctx,c,"BEGIN ISOLATION LEVEL REPEATABLE READ",0)
	_,err:=c.ExecContext(p.Ctx,"SET TRANSACTION SNAPSHOT "+snapshotQuote(token))
	want:=fixture.ControlsReferenceDiagnostic("55000","could not import the requested snapshot")
	want.Detail=fmt.Sprintf("The source process with PID %d is not running anymore.",pid)
	fixture.AssertDiagnostic(t,err,&want)
	fixture.LockExec(t,p.Ctx,c,"ROLLBACK",0)
}

func syncRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string, rows [][]any) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, rows))
}

func syncSeed(t *testing.T, p *fixture.Pair, c *sql.Conn) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, c, "CREATE TABLE sync_t(id INT PRIMARY KEY,v INT)", 0)
	fixture.LockExec(t, p.Ctx, c, "INSERT INTO sync_t VALUES(1,10),(3,30)", 2)
}

// Observe the actual advisory request, but report a completed executor's real
// outcome instead of hiding an earlier binding/admission error behind a timeout.
func syncAdvisoryWait(t *testing.T,p *fixture.Pair,observer fixture.Executor,waiter,holder int,key advisorySessionKey,completed <-chan fixture.RelationOutcome){
	t.Helper()
	ctx,cancel:=context.WithTimeout(p.Ctx,5*time.Second);defer cancel()
	for{
		select{
		case outcome:=<-completed:t.Fatalf("operation completed before advisory request: %v",outcome.Err)
		default:
		}
		for _,row:=range advisoryXactRows(t,ctx,observer,key){
			if row.PID.Valid&&row.PID.Int64==int64(waiter)&&!row.Granted&&row.Waiting&&row.Mode=="ExclusiveLock"{
				advisorySessionValue(t,ctx,observer,"SELECT $1=ANY(pg_blocking_pids($2))","BOOL",true,holder,waiter)
				return
			}
		}
		if err:=ctx.Err();err!=nil{t.Fatal("pending snapshot advisory request not observed",err)}
		runtime.Gosched()
	}
}
