package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// This observes actual server query/lock admission, not a size or sleep proxy.
// Native merge/finalizer admission probes are added with the implementation.
func orderedSetCancelAdmitted(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,stmt *sql.Stmt,q string,prepared bool) {
	t.Helper()
	ctx,cancel:=context.WithTimeout(p.Ctx,10*time.Second);defer cancel()
	peer,err:=b.DB.Conn(ctx);if err!=nil {t.Fatal(err)};defer peer.Close()
	waiter:=fixture.LockPID(t,ctx,c);blocker:=fixture.LockPID(t,ctx,peer)
	oid:=fixture.LockOID(t,ctx,"os_input")
	tx,err:=peer.BeginTx(ctx,nil);if err!=nil {t.Fatal(err)};defer tx.Rollback()
	if _,err:=tx.ExecContext(ctx,"LOCK TABLE os_input IN ACCESS EXCLUSIVE MODE");err!=nil {t.Fatal(err)}
	done:=make(chan fixture.RelationOutcome,1)
	joined:=false
	defer func(){cancel();tx.Rollback();if !joined {select {case o:=<-done:if o.Rows!=nil {o.Rows.Close()};case <-time.After(5*time.Second):t.Error("ordered-set cancellation worker did not join")}}}()
	go func(){var rows *sql.Rows;var err error;if prepared {rows,err=stmt.QueryContext(ctx)} else {rows,err=c.QueryContext(ctx,q)};done<-fixture.RelationOutcome{Rows:rows,Err:err}}()
	fixture.LockWaitBehind(t,ctx,waiter,blocker,oid,"AccessShareLock")
	var active bool
	if err:=fixture.Admin.QueryRowContext(ctx,"SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND query=$2 AND state='active' AND wait_event_type='Lock')",waiter,q).Scan(&active);err!=nil||!active {t.Fatalf("query admission %t %v",active,err)}
	var signaled bool
	if err:=fixture.Admin.QueryRowContext(ctx,"SELECT pg_cancel_backend($1)",waiter).Scan(&signaled);err!=nil||!signaled {t.Fatalf("cancel admission %t %v",signaled,err)}
	select {case o:=<-done:joined=true;if o.Rows!=nil {o.Rows.Close();t.Fatal("canceled ordered-set query published rows")};fixture.AssertDiagnostic(t,o.Err,&fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"57014",Message:"canceling statement due to user request"});case <-ctx.Done():t.Fatal("cancel join",ctx.Err())}
}
