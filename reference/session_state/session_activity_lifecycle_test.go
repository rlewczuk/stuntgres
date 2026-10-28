package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"runtime"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSessionActivitySessionsAndTimes(t *testing.T) {
	activityScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,a *sql.Conn) {
		other,err:=b.DB.Conn(p.Ctx); if err!=nil { t.Fatal(err) }; defer other.Close()
		first:=activityCapture(t,p,b,a); second:=activityCapture(t,p,b,other)
		if first.PID==second.PID || !first.Start.Equal(second.Start) || !first.Load.Equal(second.Load) { t.Fatalf("session identities/clocks %#v %#v",first,second) }
		stmt,err:=a.PrepareContext(p.Ctx,activitySQL); if err!=nil { t.Fatal(err) }; defer stmt.Close()
		for _,query:=range []string{"SET application_name = 'activity'","RESET application_name","BEGIN","COMMIT","BEGIN","ROLLBACK"} {
			activityExec(t,p,a,query); activitySameRuntime(t,first,activityCapture(t,p,b,a))
			rows,err:=stmt.QueryContext(p.Ctx); if err!=nil { t.Fatal(err) }; activitySameRuntime(t,first,activityRead(t,p.Ctx,rows,activitySQL,b.PG))
		}
		activityExec(t,p,a,"CREATE TABLE activity_sentinel (v integer)")
		activityExec(t,p,a,"INSERT INTO activity_sentinel VALUES (37)")
		activityExec(t,p,a,"SELECT pg_advisory_lock(187419::bigint)")
		defer activityExec(t,p,a,"SELECT pg_advisory_unlock_all()")
		var lockPID int64
		if err:=a.QueryRowContext(p.Ctx,"SELECT pid FROM pg_locks WHERE locktype = 'advisory' AND pid = $1 AND granted",first.PID).Scan(&lockPID); err!=nil || lockPID!=first.PID { t.Fatalf("lock identity %d %v",lockPID,err) }
		observer,err:=b.DB.Conn(p.Ctx); if err!=nil { t.Fatal(err) }; defer observer.Close()
		ctx,cancel:=context.WithCancel(p.Ctx); defer cancel()
		await:=fixture.LockOperation(t,ctx,func(ctx context.Context) fixture.RelationOutcome { rows,err:=other.QueryContext(ctx,"SELECT pg_advisory_lock(187419::bigint)"); return fixture.RelationOutcome{Rows:rows,Err:err} })
		joined := false
		defer func(){ cancel(); if !joined { out:=await(); if out.Rows!=nil { out.Rows.Close() } } }()
		for {
			var blockers string
			if err:=observer.QueryRowContext(p.Ctx,"SELECT pg_blocking_pids($1)::text",second.PID).Scan(&blockers); err!=nil { t.Fatal(err) }
			if blockers==fmt.Sprintf("{%d}",first.PID) { break }
			if err:=p.Ctx.Err(); err!=nil { t.Fatal(err) }; runtime.Gosched()
		}
		activityExec(t,p,a,"SELECT pg_advisory_unlock(187419::bigint)")
		out:=await(); joined = true; if out.Err!=nil || out.Rows==nil { t.Fatalf("lock transfer %v",out.Err) }; out.Rows.Close()
		activityExec(t,p,other,"SELECT pg_advisory_unlock(187419::bigint)")
		activitySameRuntime(t,second,activityCapture(t,p,b,other))
		// With A and the observer still borrowed, the returned idle physical owner
		// is the only reusable connection; returning it must not remint its ID.
		if err:=other.Close();err!=nil { t.Fatal(err) }
		other,err=b.DB.Conn(p.Ctx);if err!=nil { t.Fatal(err) };defer other.Close()
		activitySameRuntime(t,second,activityCapture(t,p,b,other))
		if !b.PG {
			activityExec(t,p,a,"CREATE DATABASE activity_target")
			pool:=sql.OpenDB(p.NativeConnector.WithDatabase("activity_target")); defer pool.Close()
			target,err:=pool.Conn(p.Ctx); if err!=nil { t.Fatal(err) }; defer target.Close()
			targetRuntime:=activityCapture(t,p,b,target)
			if !targetRuntime.Start.Equal(first.Start) || !targetRuntime.Load.Equal(first.Load) { t.Fatal("logical target replaced cluster event clocks") }
			// Raw's documented ErrBadConn return retires the physical owner, not just its borrower.
			if err:=other.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err,driver.ErrBadConn) { t.Fatal(err) }
			other.Close()
			replacement,err:=b.DB.Conn(p.Ctx); if err!=nil { t.Fatal(err) }; defer replacement.Close()
			fresh:=activityCapture(t,p,b,replacement)
			if fresh.PID==second.PID || fresh.PID==first.PID || !fresh.Start.Equal(first.Start) || !fresh.Load.Equal(first.Load) { t.Fatalf("replacement %#v",fresh) }
			var value int; if err:=replacement.QueryRowContext(p.Ctx,"SELECT v FROM activity_sentinel").Scan(&value); err!=nil || value!=37 { t.Fatalf("sentinel %d %v",value,err) }
		}
	})
}

func TestReferenceSessionActivityNativeControls(t *testing.T) {
	activityScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		before:=activityCapture(t,p,b,c)
		activityExec(t,p,c,"CREATE VIEW activity_runtime AS "+activitySQL)
		activityExec(t,p,c,"CREATE TABLE activity_stored (q text, pid integer, started timestamp with time zone, loaded timestamp with time zone)")
		insert:="INSERT INTO activity_stored "+activitySQL
		activityExec(t,p,c,insert)
		checkStored:=func(c *sql.Conn) {
			var got activitySnapshot
			if err:=c.QueryRowContext(p.Ctx,"SELECT q, pid, started, loaded FROM activity_stored").Scan(&got.Query,&got.PID,&got.Start,&got.Load); err!=nil { t.Fatal(err) }
			if got.Query!=insert { t.Fatalf("stored source %q",got.Query) }; activitySameRuntime(t,before,got)
		}
		checkView:=func(c *sql.Conn, want activitySnapshot) {
			query:="SELECT current_query, pg_backend_pid, pg_postmaster_start_time, pg_conf_load_time FROM activity_runtime"
			rows,err:=c.QueryContext(p.Ctx,query); if err!=nil { t.Fatal(err) }
			// Stored view columns have known native nullability; values remain ordinary typed scalars.
			defer rows.Close(); types,err:=rows.ColumnTypes(); if err!=nil { t.Fatal(err) }
			for i,typ:=range []string{"TEXT","INT4","TIMESTAMPTZ","TIMESTAMPTZ"} { if types[i].DatabaseTypeName()!=typ { t.Fatalf("view type %d %s",i,types[i].DatabaseTypeName()) } }
			var got activitySnapshot; if !rows.Next() { t.Fatal(rows.Err()) }; if err:=rows.Scan(&got.Query,&got.PID,&got.Start,&got.Load); err!=nil { t.Fatal(err) }
			if got.Query!=query { t.Fatalf("view source %q",got.Query) }; activitySameRuntime(t,want,got)
			if rows.Next() || rows.Err()!=nil { t.Fatal("view trailing row",rows.Err()) }
		}
		checkStored(c); checkView(c,before)
		if b.PG { return }
		copyConn,err:=p.Native.Conn(p.Ctx);if err!=nil { t.Fatal(err) };defer copyConn.Close()
		copySQL:="COPY (SELECT current_query()) TO STDOUT"
		var copied bytes.Buffer
		copyResult,err:=stuntgres.CopyTo(p.Ctx,copyConn,copySQL,&copied)
		if err!=nil || copyResult.Count!=1 || copyResult.CommandTag!="COPY 1" || copied.String()!=copySQL+"\n" { t.Fatalf("COPY source %q %#v %v",copied.String(),copyResult,err) }
		retained,err:=c.QueryContext(p.Ctx,activitySQL); if err!=nil { t.Fatal(err) }; defer retained.Close()
		var dump bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&dump); err!=nil { t.Fatal(err) }
		if err:=stuntgres.Reset(p.Ctx,p.Native); err!=nil { t.Fatal(err) }
		activitySameRuntime(t,before,activityCapture(t,p,b,c))
		if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes())); err!=nil { t.Fatal(err) }
		activitySameRuntime(t,before,activityCapture(t,p,b,c)); checkStored(c); checkView(c,before)
		activitySameRuntime(t,before,activityRead(t,p.Ctx,retained,activitySQL,false))
		if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewBufferString("not a dump")); err==nil { t.Fatal("invalid restore succeeded") }
		activitySameRuntime(t,before,activityCapture(t,p,b,c)); checkStored(c)
		driver:=stuntgres.NewDriver(); connector,err:=driver.OpenConnector("activity-destination"); if err!=nil { t.Fatal(err) }
		destination:=sql.OpenDB(connector); defer destination.Close()
		target,err:=destination.Conn(p.Ctx); if err!=nil { t.Fatal(err) }; defer target.Close()
		live:=activityCapture(t,p,b,target)
		if err:=stuntgres.Restore(p.Ctx,destination,bytes.NewReader(dump.Bytes())); err!=nil { t.Fatal(err) }
		activitySameRuntime(t,live,activityCapture(t,p,b,target)); checkView(target,live); checkStored(target)
	})
}
