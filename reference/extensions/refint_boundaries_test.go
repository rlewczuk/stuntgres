package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRefintPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"refint_reader"})
	refintCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
		run(fixture.Exec("CREATE ROLE refint_reader",0))
		defer func() { for _,q:=range []string{"RESET ROLE","DROP OWNED BY refint_reader","DROP ROLE refint_reader"} { if _,err:=c.ExecContext(p.Ctx,q);err!=nil { t.Error(q,err) } } }()
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO refint_reader",0))
		run(fixture.Exec("CREATE TABLE public.p(id int)",0)); run(fixture.Exec("INSERT INTO public.p VALUES(1),(2)",2))
		run(fixture.Exec("CREATE TABLE public.c(pid int)",0)); run(fixture.Exec("INSERT INTO public.c VALUES(1),(2)",2))
		run(fixture.Exec("CREATE TRIGGER rights_guard AFTER DELETE ON public.p FOR EACH ROW EXECUTE FUNCTION public.check_foreign_key('1','c','id','public.c','pid')",0))
		run(fixture.Exec("GRANT SELECT,DELETE ON public.p TO refint_reader",0))
		q:="DELETE FROM public.p WHERE id=$1"
		held,err:=c.PrepareContext(p.Ctx,q); if err!=nil { t.Fatal(err) }; defer held.Close()
		run(fixture.Exec("SET ROLE refint_reader",0))
		deniedWant := refintError("42501","permission denied for table c")
		deniedWant.Context = "SQL statement \"delete from public.c where pid = $1 \""
		run(fixture.ErrorDiagnostic("DELETE FROM public.p WHERE id=1",deniedWant))
		run(fixture.Exec("RESET ROLE",0)); refintNotices(t,notices)
		run(fixture.Select("SELECT id FROM public.p ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
		run(fixture.Exec("GRANT SELECT,DELETE ON public.c TO refint_reader",0))
		run(fixture.Exec("ALTER TABLE public.c ENABLE ROW LEVEL SECURITY",0))
		run(fixture.Exec("CREATE POLICY visible_child ON public.c TO refint_reader USING(pid=2)",0))
		run(fixture.Exec("SET ROLE refint_reader",0))
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:q},r,fixture.Exec(q,1,1))
		refintNotices(t,notices,"rights_guard: 0 tuple(s) of public.c are deleted")
		run(fixture.Exec("RESET ROLE",0))
		run(fixture.Exec("REVOKE DELETE ON public.c FROM refint_reader",0))
		run(fixture.Exec("SET ROLE refint_reader",0))
		_, denied := held.ExecContext(p.Ctx,2)
		if denied == nil { t.Fatal("held cascade retained revoked DELETE authority") }
		fixture.AssertDiagnostic(t,denied,&deniedWant)
		run(fixture.Exec("RESET ROLE",0)); refintNotices(t,notices)
		run(fixture.Select("SELECT id FROM public.p ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(2)}}))
		run(fixture.Select("SELECT pid FROM public.c ORDER BY pid",[]string{"pid"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
		run(fixture.Exec("GRANT DELETE ON public.c TO refint_reader",0))
		run(fixture.Exec("SET ROLE refint_reader",0))
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:q},r,fixture.Exec(q,1,2))
		refintNotices(t,notices,"rights_guard: 1 tuple(s) of public.c are deleted")
		run(fixture.Exec("RESET ROLE",0))
		run(fixture.Select("SELECT pid FROM public.c ORDER BY pid",[]string{"pid"},[]string{"INT4"},[][]any{{int64(1)}}))
	})
}

func TestReferenceRefintCancellation(t *testing.T) {
	refintCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
		run(fixture.Exec("CREATE TABLE p(id int)",0)); run(fixture.Exec("CREATE TABLE c(pid int)",0))
		run(fixture.Exec("INSERT INTO p VALUES(1)",1)); run(fixture.Exec("INSERT INTO c VALUES(1)",1))
		run(fixture.Exec("CREATE TRIGGER cancel_guard AFTER DELETE ON p FOR EACH ROW EXECUTE FUNCTION check_foreign_key('1','c','id','c','pid')",0))
		// The pool rejects a pre-canceled adapter call without borrowing the
		// notice owner, which must remain available for cleanup and assertions.
		ctx,cancel:=context.WithCancel(p.Ctx); cancel()
		rows,err:=b.DB.QueryContext(ctx,"DELETE FROM p RETURNING id")
		if rows!=nil { rows.Close() }
		if !errors.Is(err,context.Canceled) { t.Fatal("precanceled public adapter",err) }
		// Lock only the literal child relation: admission proves the real
		// refint nested action was reached, not just the outer DELETE.
		holder,err:=b.DB.Conn(p.Ctx); if err!=nil { t.Fatal(err) }; defer holder.Close()
		tx:=fixture.QueryLockBegin(t,p.Ctx,holder); defer tx.Rollback()
		fixture.RunStep(t,p.Ctx,tx,r,fixture.Exec("LOCK TABLE c IN ACCESS EXCLUSIVE MODE",0))
		pid,hpid:=0,0; var oid int64
		if b.PG { pid=fixture.LockPID(t,p.Ctx,c); hpid=fixture.LockPID(t,p.Ctx,holder); oid=fixture.LockOID(t,p.Ctx,"c") }
		active,stop:=context.WithCancel(p.Ctx); defer stop()
		done:=make(chan struct{})
		join:=fixture.LockOperation(t,p.Ctx,func(context.Context) fixture.RelationOutcome {
			defer close(done)
			result,err:=c.ExecContext(active,"DELETE FROM p")
			return fixture.RelationOutcome{Result:result,Err:err}
		})
		joined:=false
		defer func() { stop(); if !joined { join() } }()
		fixture.RelationLockPending(t,p,b,pid,hpid,oid,"RowExclusiveLock",done)
		if b.PG {
			var ok bool
			if err:=fixture.Admin.QueryRowContext(p.Ctx,"SELECT pg_cancel_backend($1)",pid).Scan(&ok);err!=nil || !ok { t.Fatal("cancel admitted action",ok,err) }
		} else { stop() }
		out:=join(); joined=true
		if b.PG {
			d:=refintError("57014","canceling statement due to user request")
			d.Context=`SQL statement "delete from c where pid = $1 "`
			fixture.AssertDiagnostic(t,out.Err,&d)
		} else if !errors.Is(out.Err,context.Canceled) { t.Fatal("admitted action cancellation",out.Err) }
		if err:=tx.Rollback();err!=nil { t.Fatal(err) }
		refintNotices(t,notices)
		run(fixture.Select("SELECT id FROM p",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}}))
		run(fixture.Select("SELECT pid FROM c",[]string{"pid"},[]string{"INT4"},[][]any{{int64(1)}}))
		run(fixture.Exec("DELETE FROM p",1)); refintNotices(t,notices,"cancel_guard: 1 tuple(s) of c are deleted")
	})
}

func TestReferenceRefintOutputComparison(t *testing.T) {
	refintCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
		run(fixture.Exec("CREATE TABLE p(k numeric)",0)); run(fixture.Exec("CREATE TABLE c(k numeric)",0))
		run(fixture.Exec("INSERT INTO p VALUES(1.0)",1)); run(fixture.Exec("INSERT INTO c VALUES(1.0)",1))
		// int4 input accepts surrounding whitespace, sign and base-prefix syntax.
		run(fixture.Exec("CREATE TRIGGER numeric_guard AFTER UPDATE ON p FOR EACH ROW EXECUTE FUNCTION check_foreign_key(' +0x1 ','c','k','c','k')",0))
		run(fixture.Exec("UPDATE p SET k=1.00",1)); refintNotices(t,notices,"numeric_guard: 1 tuple(s) of c are updated")
		run(fixture.Select("SELECT k::text AS k FROM c",[]string{"k"},[]string{"TEXT"},[][]any{{"1.00"}}))
		run(fixture.Exec("UPDATE p SET k=1.00",1)); refintNotices(t,notices)
	})
}
