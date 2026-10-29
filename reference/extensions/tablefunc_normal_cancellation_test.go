package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

type normalCancelResult struct { result sql.Result; err error }
func normalCancelWorker(ctx context.Context,c *sql.Conn,st *sql.Stmt,q string,done chan<- normalCancelResult) {
	var r normalCancelResult
	if st!=nil {r.result,r.err=st.ExecContext(ctx)} else {r.result,r.err=c.ExecContext(ctx,q)}
	done<-r
}
func normalAdmittedCancel(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,prepared bool) {
	t.Helper()
	const q="SELECT count(*) FROM normal_part.normal_rand(2147483647,0,1)"
	var st *sql.Stmt
	var err error
	if prepared {st,err=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer st.Close()}
	var observer *sql.Conn
	var pid int64
	if b.PG {
		if err:=c.QueryRowContext(p.Ctx,"SELECT pg_backend_pid()").Scan(&pid);err!=nil{t.Fatal(err)}
		observer,err=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer observer.Close()
	} else {
		ctx,cancel:=context.WithCancel(p.Ctx);cancel()
		var rows *sql.Rows
		if st!=nil {rows,err=st.QueryContext(ctx)} else {rows,err=c.QueryContext(ctx,q)}
		if rows!=nil{rows.Close();t.Fatal("published precanceled rows")};if !errors.Is(err,context.Canceled){t.Fatal("precancellation",err)}
	}
	ctx,cancel:=context.WithCancel(p.Ctx);defer cancel()
	done:=make(chan normalCancelResult,1)
	go normalCancelWorker(ctx,c,st,q,done)
	joined:=false
	defer func(){if !joined{cancel();timer:=time.NewTimer(5*time.Second);defer timer.Stop();select{case <-done:case <-timer.C:t.Error("normal worker failed to join")}}}()
	buffer:=make([]byte,1<<20)
	for {
		admitted:=false
		if b.PG {
			if err:=observer.QueryRowContext(p.Ctx,"SELECT state='active' AND query=$2 FROM pg_stat_activity WHERE pid=$1",pid,q).Scan(&admitted);err!=nil{t.Fatal(err)}
		} else {
			n:=runtime.Stack(buffer,true)
			for stack:=range bytes.SplitSeq(buffer[:n],[]byte("\n\n")) {
				if bytes.Contains(stack,[]byte("normalCancelWorker"))&&(bytes.Contains(stack,[]byte("nextTablefuncNormal"))||bytes.Contains(stack,[]byte("SourceCursor).Next"))){admitted=true;break}
			}
		}
		if admitted{break}
		select{case r:=<-done:joined=true;t.Fatal("completed before provider admission",r.err);case <-p.Ctx.Done():t.Fatal(p.Ctx.Err());default:runtime.Gosched()}
	}
	if b.PG {var canceled bool;if err:=observer.QueryRowContext(p.Ctx,"SELECT pg_cancel_backend($1)",pid).Scan(&canceled);err!=nil||!canceled{t.Fatal(canceled,err)}} else {cancel()}
	timer:=time.NewTimer(5*time.Second);defer timer.Stop()
	select{
	case r:=<-done:
		joined=true
		if r.result!=nil{t.Fatal("published canceled result")}
		if b.PG{want:=fixture.ControlsReferenceDiagnostic("57014","canceling statement due to user request");fixture.AssertDiagnostic(t,r.err,&want)} else if !errors.Is(r.err,context.Canceled){t.Fatal(r.err)}
	case <-timer.C:t.Fatal("cancellation worker failed to join")
	}
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.TablefuncNormalSelect("SELECT * FROM normal_part.normal_rand(2,9,0)",2,9))
}
func TestReferenceTablefuncNormalCancellation(t *testing.T){
	normalCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){for _,prepared:=range []bool{false,true}{normalAdmittedCancel(t,p,b,c,prepared)}})
}
