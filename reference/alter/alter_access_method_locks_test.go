package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterAccessMethodCancellation(t *testing.T) {
	for _,prepared:=range []bool{false,true} {t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T) {
		accessMethodScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			persistenceExec(t,p,b,c,"CREATE TABLE t(id int,note text)")
			persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO t VALUES(7,'keep')",1))
			holder,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer holder.Close()
			observer,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer observer.Close()
			query:="ALTER TABLE t SET ACCESS METHOD heap2, ADD COLUMN added int DEFAULT 9"
			var held *sql.Stmt
			if prepared {held,err=c.PrepareContext(p.Ctx,query);if err!=nil {t.Fatal(err)};defer held.Close()}
			workerPID,holderPID:=0,0
			var oid int64
			if b.PG {workerPID=fixture.LockPID(t,p.Ctx,c);holderPID=fixture.LockPID(t,p.Ctx,holder);oid=fixture.LockOID(t,p.Ctx,"t")}
			tx,err:=holder.BeginTx(p.Ctx,nil);if err!=nil {t.Fatal(err)};defer tx.Rollback()
			persistenceExec(t,p,b,tx,"LOCK TABLE t IN ACCESS SHARE MODE")
			ctx,cancel:=context.WithCancel(p.Ctx);defer cancel()
			finished:=make(chan struct{})
			join:=fixture.LockOperation(t,ctx,func(ctx context.Context) fixture.RelationOutcome {
				defer close(finished)
				var result sql.Result;var err error
				if prepared {result,err=held.ExecContext(ctx)} else {result,err=c.ExecContext(ctx,query)}
				return fixture.RelationOutcome{Result:result,Err:err}
			})
			fixture.RelationLockPending(t,p,b,workerPID,holderPID,oid,"AccessExclusiveLock",finished)
			accessMethodProperty(t,p,b,observer,"t","heap")
			if b.PG {
				var canceled bool
				if err=fixture.Admin.QueryRowContext(p.Ctx,"SELECT pg_cancel_backend($1)",workerPID).Scan(&canceled);err!=nil || !canceled {t.Fatal(canceled,err)}
			} else {cancel()}
			out:=join()
			if out.Result!=nil || out.Rows!=nil {t.Fatal("canceled ALTER exposed result")}
			if b.PG {want:=persistenceDiagnostic("57014","canceling statement due to user request");fixture.AssertDiagnostic(t,out.Err,&want)} else if !errors.Is(out.Err,context.Canceled) {t.Fatal(out.Err)}
			if err=tx.Rollback();err!=nil {t.Fatal(err)}
			accessMethodProperty(t,p,b,observer,"t","heap")
			persistenceRun(t,p,b,observer,fixture.Select("SELECT * FROM t",[]string{"id","note"},[]string{"INT4","TEXT"},[][]any{{int64(7),"keep"}}))
			persistenceRun(t,p,b,observer,fixture.Select("SELECT count(*) FROM pg_locks WHERE NOT granted",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
			persistenceExec(t,p,b,observer,"ALTER TABLE t SET ACCESS METHOD heap2")
			accessMethodProperty(t,p,b,observer,"t","heap2")
		})
	})}
}
