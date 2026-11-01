package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

// Every failure uses the real public ALTER executor; the advisory queue is an
// observed expression boundary, not a timer or a replacement evaluator.
func TestReferenceSnapshotSyncAlterCancellation(t *testing.T) {
	for _, held := range []bool{false, true} {
		name := "direct"; if held { name = "held" }
		t.Run(name, func(t *testing.T) { snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, holder, c, observer := snapshotConn(t,p,b), snapshotConn(t,p,b), snapshotConn(t,p,b), snapshotConn(t,p,b)
			syncSeed(t,p,holder)
			fixture.LockExec(t,p.Ctx,holder,"CREATE TABLE sync_alter_cancel(id INT)",0)
			fixture.LockExec(t,p.Ctx,holder,"INSERT INTO sync_alter_cancel VALUES(1),(2)",2)
			const command = "ALTER TABLE sync_alter_cancel ADD COLUMN added INT DEFAULT 7, ADD CONSTRAINT interrupted CHECK ((CASE WHEN set_config('sync.token',pg_export_snapshot(),false) IS NOT NULL THEN pg_advisory_xact_lock(4294967298::bigint) ELSE pg_advisory_xact_lock(4294967298::bigint) END) IS NULL)"
			var statement *sql.Stmt
			if held { var err error; statement,err=a.PrepareContext(p.Ctx,command); if err!=nil {t.Fatal(err)}; defer statement.Close() }
			pid,holderPID:=fixture.LockPID(t,p.Ctx,a),fixture.LockPID(t,p.Ctx,holder)
			fixture.LockExec(t,p.Ctx,a,"BEGIN",0)
			old:=syncExport(t,p,b,a,nil)
			fixture.LockExec(t,p.Ctx,a,"INSERT INTO sync_alter_cancel VALUES(9)",1)
			fixture.LockExec(t,p.Ctx,holder,"SELECT pg_advisory_lock(4294967298::bigint)",1)
			ctx,cancel:=context.WithCancel(p.Ctx);defer cancel()
			join:=fixture.LockOperation(t,ctx,func(ctx context.Context)fixture.RelationOutcome { var result sql.Result; var err error; if held {result,err=statement.ExecContext(ctx)} else {result,err=a.ExecContext(ctx,command)}; return fixture.RelationOutcome{Result:result,Err:err} })
			advisoryXactWait(t,p,observer,pid,holderPID,advisorySessionKeys[0],false)
			if b.PG {
				var signaled bool
				if err:=fixture.Admin.QueryRowContext(p.Ctx,"SELECT pg_cancel_backend($1)",pid).Scan(&signaled);err!=nil||!signaled{t.Fatal(signaled,err)}
			} else {cancel()}
			out:=join()
			if b.PG {want:=fixture.ControlsReferenceDiagnostic("57014","canceling statement due to user request");fixture.AssertDiagnostic(t,out.Err,&want)} else if !errors.Is(out.Err,context.Canceled){t.Fatal("lost ALTER caller cancellation",out.Err)}
			fixture.LockExec(t,p.Ctx,holder,"SELECT pg_advisory_unlock(4294967298::bigint)",1)
			syncAborted(t,p,c,old,pid)
			fixture.LockExec(t,p.Ctx,a,"ROLLBACK",0);syncExpired(t,p,c,old)
			fixture.RunStep(t,p.Ctx,holder,b.Recorder,fixture.Select("SELECT id FROM sync_alter_cancel ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
			fixture.RunStep(t,p.Ctx,holder,b.Recorder,fixture.Select("SELECT count(*) FROM pg_attribute WHERE attrelid='sync_alter_cancel'::regclass AND attname='added'",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
			fixture.RunStep(t,p.Ctx,holder,b.Recorder,fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='sync_alter_cancel'::regclass AND conname='interrupted'",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
		}) })
	}
}

func TestReferenceSnapshotSyncAlterErrorOrder(t *testing.T) {
	for _,route:=range []string{"child-conversion","child-binding"} {
	for _, held := range []bool{false,true} {
		name:=route+"-direct"; if held {name=route+"-held"}
		t.Run(name,func(t *testing.T){snapshotScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			a,c:=snapshotConn(t,p,b),snapshotConn(t,p,b)
			pid:=fixture.LockPID(t,p.Ctx,a)
			for _,q:=range []string{"CREATE SEQUENCE sync_alter_order_seq", "CREATE TABLE sync_alter_order(id INT,v TEXT)", "CREATE TABLE sync_alter_order_child() INHERITS(sync_alter_order)", "INSERT INTO sync_alter_order VALUES(1,'10')", "INSERT INTO sync_alter_order_child VALUES(2,'bad')", "CREATE FUNCTION sync_alter_order_fill() RETURNS INT STABLE LANGUAGE SQL AS $$SELECT nextval('sync_alter_order_seq')::int$$"} { if _,err:=a.ExecContext(p.Ctx,q);err!=nil{t.Fatal(err)} }
			// A saved child ADD diagnostic is replayed in the child's stage,
			// after the parent's nonvolatile DEFAULT preflight. Do not expose
			// it early merely to collect possible snapshot demand.
			command:="ALTER TABLE sync_alter_order ADD COLUMN fill INT DEFAULT sync_alter_order_fill(), ALTER COLUMN v TYPE INT USING v::int"
			want:=fixture.ControlsReferenceDiagnostic("22P02",`invalid input syntax for type integer: "bad"`)
			if route=="child-binding" {
				fixture.LockExec(t,p.Ctx,a,"ALTER TABLE ONLY sync_alter_order_child ADD COLUMN fill TEXT",0)
				command="ALTER TABLE sync_alter_order ADD COLUMN fill INT DEFAULT sync_alter_order_fill()"
				want=fixture.ControlsReferenceDiagnostic("42804",`child table "sync_alter_order_child" has different type for column "fill"`)
			}
			var statement *sql.Stmt
			if held {var err error;statement,err=a.PrepareContext(p.Ctx,command);if err!=nil{t.Fatal(err)};defer statement.Close()}
			fixture.LockExec(t,p.Ctx,a,"BEGIN",0);token:=syncExport(t,p,b,a,nil)
			var err error; if held {_,err=statement.ExecContext(p.Ctx)} else {_,err=a.ExecContext(p.Ctx,command)}
			fixture.AssertDiagnostic(t,err,&want)
			syncAborted(t,p,c,token,pid);fixture.LockExec(t,p.Ctx,a,"ROLLBACK",0);syncExpired(t,p,c,token)
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT is_called FROM sync_alter_order_seq",[]string{"is_called"},[]string{"BOOL"},[][]any{{true}}))
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT id,v FROM sync_alter_order ORDER BY id",[]string{"id","v"},[]string{"INT4","TEXT"},[][]any{{int64(1),"10"},{int64(2),"bad"}}))
			count:=int64(0);if route=="child-binding"{count=1}
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT count(*) FROM pg_attribute WHERE attrelid IN ('sync_alter_order'::regclass,'sync_alter_order_child'::regclass) AND attname='fill'",[]string{"count"},[]string{"INT8"},[][]any{{count}}))
		})})
	}
	}
}
