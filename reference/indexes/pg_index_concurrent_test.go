package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGIndexConcurrentState(t *testing.T) {
	for _, phase := range []string{"before","first","snapshot"} {
		for _, cancelBuild := range []bool{false,true} {
			name := phase+"/complete"; if cancelBuild { name = phase+"/cancel" }
			t.Run(name,func(t *testing.T) {
				fixture.ConcurrentIndexScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
					a,c,builder := fixture.ConcurrentIndexConn(t,p,b),fixture.ConcurrentIndexConn(t,p,b),fixture.ConcurrentIndexConn(t,p,b)
					run := transactionalIndexRun(t,p,b,c)
					for _,q := range []string{"CREATE TABLE pgi_concurrent(n INT)","CREATE TABLE pgi_snapshot(n INT)"} { run(fixture.Exec(q,0)) }
					run(fixture.Exec("INSERT INTO pgi_concurrent VALUES(1)",1))
					ap,bp := fixture.ConcurrentIndexPID(t,p,b,a),fixture.ConcurrentIndexPID(t,p,b,builder)
					owner := transactionalIndexRun(t,p,b,a)
					if phase == "snapshot" {
						owner(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ",0))
						owner(fixture.Select("SELECT n FROM pgi_snapshot",[]string{"n"},[]string{"INT4"},nil))
					} else {
						owner(fixture.Exec("BEGIN",0))
						if phase == "before" { owner(fixture.Exec("LOCK TABLE pgi_concurrent IN SHARE MODE",0)) } else { owner(fixture.Exec("INSERT INTO pgi_concurrent VALUES(2)",1)) }
					}
					var join func() error
					var cancel context.CancelFunc
					if phase == "before" {
						ctx,stop := context.WithCancel(p.Ctx); defer stop(); cancel = stop
						finished := make(chan struct{})
						wait := fixture.LockOperation(t,ctx,func(ctx context.Context) fixture.RelationOutcome {
							defer close(finished)
							r,err := builder.ExecContext(ctx,"CREATE UNIQUE INDEX CONCURRENTLY pgi_concurrent_ix ON pgi_concurrent(n)")
							return fixture.RelationOutcome{Result:r,Err:err}
						})
						join = func() error { out := wait(); if out.Err == nil { n,err := out.Result.RowsAffected(); if err != nil || n != 0 { t.Fatalf("build affected=%d err=%v",n,err) } }; return out.Err }
						var rel int64
						if b.PG { rel = fixture.LockOID(t,p.Ctx,"pgi_concurrent") }
						fixture.RelationLockPending(t,p,b,bp,ap,rel,"ShareUpdateExclusiveLock",finished)
					} else {
						w := fixture.ConcurrentIndexStart(t,p,builder,"CREATE UNIQUE INDEX CONCURRENTLY pgi_concurrent_ix ON pgi_concurrent(n)",true)
						cancel = w.Cancel; join = func() error { return w.Join(t,p) }
						want := stuntgres.IndexState{Live:true}; wait := "waiting for writers before build"
						if phase == "snapshot" { want.Ready = true; wait = "waiting for old snapshots" }
						fixture.ConcurrentIndexWait(t,p,b,"pgi_concurrent_ix",want,bp,ap,wait)
					}
					var oid int64
					var expected [][]any
					if phase != "before" {
						row := pgIndexOrdinary(1,1,"1","0","1978","0",nil,nil)
						row[4],row[10],row[12] = true,false,phase == "snapshot"; expected = [][]any{row}
						if err := c.QueryRowContext(p.Ctx,"SELECT 'pgi_concurrent_ix'::regclass::oid::bigint").Scan(&oid); err != nil || oid <= 0 { t.Fatal("published identity",oid,err) }
					}
					pgIndexBoth(run,pgIndexLogical("pgi_concurrent_ix",expected))
					if cancelBuild {
						if b.PG {
							var canceled bool
							if err := fixture.Admin.QueryRowContext(p.Ctx,"SELECT pg_cancel_backend($1)",bp).Scan(&canceled); err != nil || !canceled { t.Fatal("cancel",canceled,err) }
						} else { cancel() }
						err := join()
						if b.PG { want := fixture.ControlsReferenceDiagnostic("57014","canceling statement due to user request"); fixture.AssertDiagnostic(t,err,&want) } else if !errors.Is(err,context.Canceled) { t.Fatal("caller cancellation",err) }
						pgIndexBoth(run,pgIndexLogical("pgi_concurrent_ix",expected))
						if phase != "before" {
							pgIndexBoth(run,fixture.Select("SELECT indexrelid=$1::oid AS retained FROM pg_index WHERE indexrelid='pgi_concurrent_ix'::regclass",[]string{"retained"},[]string{"BOOL"},[][]any{{true}},oid))
							if !b.PG {
								var out fixture.ConcurrentIndexCountingWriter
								err := stuntgres.Dump(p.Ctx,p.Native,&out)
								fixture.TransactionOrdinary(t,err,`stuntgres: dump cannot serialize invalid index "public"."pgi_concurrent_ix"`)
								if out.Calls != 0 || out.Bytes != 0 { t.Fatal("invalid Dump wrote",out) }
							}
							if phase == "snapshot" { run(fixture.ErrorDiagnostic("INSERT INTO pgi_concurrent VALUES(1)",fixture.UniqueReferenceDuplicate("pgi_concurrent","pgi_concurrent_ix","Key (n)=(1) already exists."))) } else { run(fixture.Exec("INSERT INTO pgi_concurrent VALUES(1)",1)) }
							if phase == "first" { owner(fixture.Exec("ROLLBACK",0)) }
							run(fixture.Exec("DROP INDEX pgi_concurrent_ix",0)); pgIndexBoth(run,pgIndexLogical("pgi_concurrent_ix",nil))
						}
						if phase != "first" { owner(fixture.Exec("ROLLBACK",0)) }
					} else {
						owner(fixture.Exec("COMMIT",0)); if err := join(); err != nil { t.Fatal(err) }
						row := pgIndexOrdinary(1,1,"1","0","1978","0",nil,nil); row[4] = true
						pgIndexBoth(run,pgIndexLogical("pgi_concurrent_ix",[][]any{row}))
						if oid != 0 { pgIndexBoth(run,fixture.Select("SELECT indexrelid=$1::oid AS retained FROM pg_index WHERE indexrelid='pgi_concurrent_ix'::regclass",[]string{"retained"},[]string{"BOOL"},[][]any{{true}},oid)) }
						run(fixture.ErrorDiagnostic("INSERT INTO pgi_concurrent VALUES(1)",fixture.UniqueReferenceDuplicate("pgi_concurrent","pgi_concurrent_ix","Key (n)=(1) already exists.")))
					}
				})
			})
		}
	}
}
