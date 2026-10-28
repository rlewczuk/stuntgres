package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func persistenceSelfCancellationCases(t *testing.T) {
	for _,prepared:=range []bool{false,true} {
		t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T) {
			persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				persistenceExec(t,p,b,c,"CREATE SEQUENCE self_seq CACHE 5")
				persistenceValue(t,p,b,c,"nextval('self_seq')",1)
				holder,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};t.Cleanup(func(){holder.Close()})
				observer,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};t.Cleanup(func(){observer.Close()})
				query:="ALTER TABLE self_seq SET UNLOGGED"
				var held *sql.Stmt
				if prepared {held,err=c.PrepareContext(p.Ctx,query);if err!=nil {t.Fatal(err)};t.Cleanup(func(){held.Close()})}
				workerPID,holderPID:=0,0;var oid int64
				if b.PG {workerPID=fixture.LockPID(t,p.Ctx,c);holderPID=fixture.LockPID(t,p.Ctx,holder);oid=fixture.LockOID(t,p.Ctx,"self_seq")}
				tx,err:=holder.BeginTx(p.Ctx,nil);if err!=nil {t.Fatal(err)};defer tx.Rollback()
				// RX survives until this transaction ends, but nextval's shared
				// generator reservation survives rollback independently of DDL.
				persistenceValue(t,p,b,tx,"nextval('self_seq')",6)
				ctx,cancel:=context.WithCancel(p.Ctx);defer cancel()
				finished:=make(chan struct{})
				join:=fixture.LockOperation(t,ctx,func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					var result sql.Result;var err error
					if prepared {result,err=held.ExecContext(ctx)} else {result,err=c.ExecContext(ctx,query)}
					return fixture.RelationOutcome{Result:result,Err:err}
				})
				fixture.RelationLockPending(t,p,b,workerPID,holderPID,oid,"AccessExclusiveLock",finished)
				persistenceProperty(t,p,b,observer,"self_seq","p")
				if b.PG {var ok bool;if err=fixture.Admin.QueryRowContext(p.Ctx,"SELECT pg_cancel_backend($1)",workerPID).Scan(&ok);err!=nil || !ok {t.Fatal(ok,err)}} else {cancel()}
				out:=join()
				if out.Result!=nil || out.Rows!=nil {t.Fatal("self persistence cancel published result")}
				if b.PG {want:=persistenceDiagnostic("57014","canceling statement due to user request");fixture.AssertDiagnostic(t,out.Err,&want)} else if !errors.Is(out.Err,context.Canceled) {t.Fatal(out.Err)}
				if err=tx.Rollback();err!=nil {t.Fatal(err)}
				persistenceProperty(t,p,b,observer,"self_seq","p")
				persistenceRun(t,p,b,observer,fixture.Select("SELECT last_value,is_called FROM self_seq",[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(10),true}}))
				persistenceRun(t,p,b,observer,fixture.Select("SELECT count(*) FROM pg_locks WHERE NOT granted",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
				persistenceValue(t,p,b,c,"currval('self_seq')",1)
				persistenceValue(t,p,b,c,"lastval()",1)
				persistenceValue(t,p,b,c,"nextval('self_seq')",2)
				if prepared {persistenceRun(t,p,b,fixture.HeldStatement{Executor:c,Statement:held,SQL:query},fixture.Exec(query,0))} else {persistenceExec(t,p,b,c,query)}
				persistenceProperty(t,p,b,c,"self_seq","u")
				persistenceValue(t,p,b,c,"currval('self_seq')",2)
				persistenceValue(t,p,b,c,"nextval('self_seq')",11)
			})
		})
	}
}
