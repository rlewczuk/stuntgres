package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func backendMemoryCancel(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,full bool) {
	t.Helper()
	for _,source:=range []string{"pg_catalog.pg_backend_memory_contexts","pg_catalog.pg_get_backend_memory_contexts()"} {
		q:="SELECT name,pg_sleep(30) FROM "+source+" LIMIT 1"
		for _,prepared:=range []bool{false,true} {
			var stmt *sql.Stmt
			if prepared {var err error;stmt,err=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()}
			fixture.SleepActiveCancel(t,p,b,c,q,nil,func(ctx context.Context) fixture.RelationOutcome {
				var rows *sql.Rows;var err error
				if prepared {rows,err=stmt.QueryContext(ctx)}else{rows,err=c.QueryContext(ctx,q)}
				return fixture.RelationOutcome{Rows:rows,Err:err}
			})
			backendMemoryRead(t,p,b,c,backendMemoryView,false)
			backendMemoryRead(t,p,b,c,backendMemoryFunction,false)
			backendMemorySentinel(t,p,b,c)
		}
	}
	if !full||b.PG{return}
	// Native Go/context errors remain ordinary, not fabricated SQL diagnostics.
	for _,q:=range []string{backendMemoryView,backendMemoryFunction} {
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
		for _,expired:=range []bool{false,true} {
			var ctx context.Context;var cancel context.CancelFunc;want:=error(context.Canceled)
			if expired {ctx,cancel=context.WithDeadline(p.Ctx,time.Now().Add(-time.Second));want=context.DeadlineExceeded}else{ctx,cancel=context.WithCancel(p.Ctx);cancel()}
			rows,err:=c.QueryContext(ctx,q)
			if rows!=nil{rows.Close();t.Fatal("canceled direct source published rows")};fixture.LexicalOrdinary(t,err,want)
			rows,err=stmt.QueryContext(ctx)
			if rows!=nil{rows.Close();t.Fatal("canceled held source published rows")};fixture.LexicalOrdinary(t,err,want)
			fresh,err:=c.PrepareContext(ctx,q)
			if fresh!=nil{fresh.Close();t.Fatal("canceled prepare published statement")};fixture.LexicalOrdinary(t,err,want)
			cancel()
			backendMemorySentinel(t,p,b,c)
		}
	}
}

func TestReferenceBackendMemoryCancellation(t *testing.T) {
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){c:=fixture.SequenceCoreConn(t,p.Ctx,b.DB);backendMemorySetupSentinel(t,p,b,c);backendMemoryCancel(t,p,b,c,true)})}
}

func TestReferenceBackendMemoryFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"backend_memory_actor"})
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
		a,z:=fixture.LockConnections(t,p.Ctx,b.DB)
		backendMemorySetupSentinel(t,p,b,z)
		for _,q:=range []string{backendMemoryView,backendMemoryFunction}{for _,prepared:=range []bool{false,true}{backendMemoryRead(t,p,b,a,q,prepared)}}
		backendMemoryOwnership(t,p,b,a,z,false)
		backendMemoryAuthorization(t,p,b,a,z,false)
		pgSequenceExec(t,p,b,z,"GRANT SELECT ON pg_catalog.pg_backend_memory_contexts TO backend_memory_actor")
		defer z.ExecContext(context.Background(),"REVOKE SELECT ON pg_catalog.pg_backend_memory_contexts FROM backend_memory_actor")
		backendMemoryControlSmoke(t,p,b,a)
		backendMemoryCancel(t,p,b,a,false)
		backendMemorySentinel(t,p,b,z)
		if b.PG {
			fmt.Fprintln(os.Stderr,"BACKEND-MEMORY postgres: callable/view 10 typed fields; one-capture tree and own allocator invariants; populated TEMP named row/array storage; held revoke 42501; sentinel=42; admitted cancellation 57014")
		}else{
			fmt.Fprintln(os.Stderr,"BACKEND-MEMORY memory: callable/view 10 typed fields; uniform own-layout delta and A/B isolation; allocator counters NULL; populated TEMP named row/array survives replacement; held revoke 42501; controls sentinel=42; admitted context cancellation")
		}
	})}
}
