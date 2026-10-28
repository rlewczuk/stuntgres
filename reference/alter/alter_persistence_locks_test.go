package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterPersistenceCancellation(t *testing.T) {
	t.Run("self-sequence",persistenceSelfCancellationCases)
	for _, prepared:=range []bool{false,true} {
		for _, sequence:=range []bool{false,true} {
			t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared]+map[bool]string{false:"table",true:"sequence"}[sequence],func(t *testing.T) {
				persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
					persistenceExec(t,p,b,c,"CREATE TABLE t(id serial PRIMARY KEY,note text)")
					persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO t VALUES(7,'keep')",1))
					holder,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};t.Cleanup(func(){holder.Close()})
					observer,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};t.Cleanup(func(){observer.Close()})
					// Native Rows are eager detached snapshots; pq Rows would itself
					// retain AS and mask the owned-sequence admission witness.
					var retained *sql.Rows
					if !b.PG {
						retained,err=observer.QueryContext(p.Ctx,"SELECT id,note FROM t")
						if err!=nil {t.Fatal(err)}
						t.Cleanup(func(){retained.Close()})
					}
					var held *sql.Stmt
					q:="ALTER TABLE t SET UNLOGGED"
					if prepared {held,err=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};t.Cleanup(func(){held.Close()})}
					workerPID,holderPID:=0,0
					var oid int64
					if b.PG {workerPID=fixture.LockPID(t,p.Ctx,c);holderPID=fixture.LockPID(t,p.Ctx,holder);name:="t";if sequence {name="t_id_seq"};oid=fixture.LockOID(t,p.Ctx,name)}
					tx,err:=holder.BeginTx(p.Ctx,nil);if err!=nil {t.Fatal(err)};defer tx.Rollback()
					if sequence {persistenceExec(t,p,b,tx,"ALTER SEQUENCE t_id_seq CACHE 2")} else {persistenceExec(t,p,b,tx,"LOCK TABLE t IN ACCESS SHARE MODE")}
					ctx,cancel:=context.WithCancel(p.Ctx);defer cancel()
					finished:=make(chan struct{})
					join:=fixture.LockOperation(t,ctx,func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						var result sql.Result;var err error
						if prepared {result,err=held.ExecContext(ctx)} else {result,err=c.ExecContext(ctx,q)}
						return fixture.RelationOutcome{Result:result,Err:err}
					})
					fixture.RelationLockPending(t,p,b,workerPID,holderPID,oid,"AccessExclusiveLock",finished)
					for _,name:=range []string{"t","t_pkey","t_id_seq"} {persistenceProperty(t,p,b,observer,name,"p")}
					if b.PG {
						var canceled bool
						if err=fixture.Admin.QueryRowContext(p.Ctx,"SELECT pg_cancel_backend($1)",workerPID).Scan(&canceled);err!=nil || !canceled {t.Fatal(canceled,err)}
					} else {cancel()}
					out:=join()
					if out.Result!=nil || out.Rows!=nil {t.Fatal("canceled ALTER published result")}
					if b.PG {want:=persistenceDiagnostic("57014","canceling statement due to user request");fixture.AssertDiagnostic(t,out.Err,&want)} else if !errors.Is(out.Err,context.Canceled) {t.Fatal(out.Err)}
					if err=tx.Rollback();err!=nil {t.Fatal(err)}
					for _,name:=range []string{"t","t_pkey","t_id_seq"} {persistenceProperty(t,p,b,observer,name,"p")}
					if retained!=nil {
						columns,err:=retained.Columns();if err!=nil || len(columns)!=2 || columns[0]!="id" || columns[1]!="note" {t.Fatal(columns,err)}
						types,err:=retained.ColumnTypes();if err!=nil || len(types)!=2 || types[0].DatabaseTypeName()!="INT4" || types[1].DatabaseTypeName()!="TEXT" {t.Fatal(types,err)}
						if !retained.Next() {t.Fatal("lost retained row",retained.Err())}
						var id int64;var note string
						if err=retained.Scan(&id,&note);err!=nil || id!=7 || note!="keep" {t.Fatal(id,note,err)}
						if retained.Next() || retained.Err()!=nil {t.Fatal("retained extra row/error",retained.Err())}
						if err=retained.Close();err!=nil {t.Fatal(err)}
					}
					persistenceRun(t,p,b,observer,fixture.Select("SELECT * FROM t",[]string{"id","note"},[]string{"INT4","TEXT"},[][]any{{int64(7),"keep"}}))
					persistenceRun(t,p,b,observer,fixture.Select("SELECT count(*) FROM pg_locks WHERE NOT granted",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
					persistenceExec(t,p,b,observer,q)
					for _,name:=range []string{"t","t_pkey","t_id_seq"} {persistenceProperty(t,p,b,observer,name,"u")}
				})
			})
		}
	}
}
