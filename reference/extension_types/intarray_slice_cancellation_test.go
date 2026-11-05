package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceIntarraySliceCancellation(t *testing.T) {
	t.Run("native_terminal_contexts",func(t *testing.T) {
		intarrayScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			intarraySliceSeed(t,p,b,c)
			// lib/pq has no pre-admission SQL diagnostic contract. The shared
			// admitted cases below exercise genuine PostgreSQL cancellation.
			if b.PG { intarrayBoth(t,p,b,c,intarraySliceStoredCheck(b)); return }
			for _,tc := range intarraySliceCases {
				q := "SELECT "+tc.expression(tc.parameters(true))+" AS v"
				stmt,err := c.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
				for _,prepared := range []bool{false,true} {
					for _,expired := range []bool{false,true} {
						ctx,cancel := context.WithCancel(p.Ctx)
						want := error(context.Canceled)
						if expired {
							cancel()
							ctx,cancel = context.WithDeadline(p.Ctx,time.Now().Add(-time.Second))
							want = context.DeadlineExceeded
						} else { cancel() }
						var rows *sql.Rows
						if prepared { rows,err = stmt.QueryContext(ctx,tc.args...) } else { rows,err = c.QueryContext(ctx,q,tc.args...) }
						cancel()
						if rows != nil { rows.Close(); t.Fatal("terminal context returned rows") }
						var state interface{ SQLState() string }
						if !errors.Is(err,want) || errors.As(err,&state) { t.Fatalf("ordinary terminal error %v want %v",err,want) }
						intarrayBoth(t,p,b,c,intarraySliceStoredCheck(b))
					}
				}
				if err := stmt.Close(); err != nil { t.Fatal(err) }
			}
		})
	})
	for _,operation := range []string{"idx","subarray"} {
		for _,prepared := range []bool{false,true} {
			t.Run(fmt.Sprintf("admitted_%s/prepared_%t",operation,prepared),func(t *testing.T) {
				intarrayScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
					intarraySliceSeed(t,p,b,c)
					q := "UPDATE ia_slice_rows SET a=ia_provider.subarray(a,2) WHERE id<3"
					if operation == "idx" { q = "INSERT INTO ia_slice_rows(id,a) SELECT id+10,ia_provider.intset(ia_provider.idx(a,22)) FROM ia_slice_rows WHERE id<3" }
					var stmt *sql.Stmt
					if prepared {
						var err error
						stmt,err = c.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
					}
					holder,observer := fixture.LockConnections(t,p.Ctx,b.DB)
					waiterPID,holderPID := 0,0; var oid int64
					if b.PG {
						waiterPID,holderPID = fixture.LockPID(t,p.Ctx,c),fixture.LockPID(t,p.Ctx,holder)
						oid = fixture.LockOID(t,p.Ctx,"ia_slice_rows")
					}
					block,err := holder.BeginTx(p.Ctx,nil); if err != nil { t.Fatal(err) }; defer block.Rollback()
					fixture.RunStep(t,p.Ctx,block,b.Recorder,fixture.Exec("LOCK TABLE ia_slice_rows IN ACCESS EXCLUSIVE MODE",0))
					ctx,cancel := context.WithCancel(p.Ctx); defer cancel()
					finished := make(chan struct{})
					await := fixture.LockOperation(t,ctx,func(ctx context.Context) fixture.RelationOutcome {
						var result sql.Result; var err error
						if prepared { result,err = stmt.ExecContext(ctx) } else { result,err = c.ExecContext(ctx,q) }
						close(finished)
						return fixture.RelationOutcome{Result:result,Err:err}
					})
					fixture.RelationLockPending(t,p,b,waiterPID,holderPID,oid,"RowExclusiveLock",finished)
					if b.PG {
						var admitted bool
						if err := fixture.Admin.QueryRowContext(p.Ctx,"SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND query=$2 AND state='active' AND wait_event_type='Lock')",waiterPID,q).Scan(&admitted); err != nil || !admitted { t.Fatalf("selected SQL admission %t: %v",admitted,err) }
						var signaled bool
						if err := fixture.Admin.QueryRowContext(p.Ctx,"SELECT pg_cancel_backend($1)",waiterPID).Scan(&signaled); err != nil || !signaled { t.Fatalf("cancel %t: %v",signaled,err) }
					} else { cancel() }
					out := await()
					if out.Result != nil { t.Fatal("canceled selected write returned Result") }
					if b.PG {
						want := fixture.ControlsReferenceDiagnostic("57014","canceling statement due to user request")
						fixture.AssertDiagnostic(t,out.Err,&want)
					} else {
						var state interface{ SQLState() string }
						if !errors.Is(out.Err,context.Canceled) || errors.As(out.Err,&state) { t.Fatal(out.Err) }
					}
					if err := block.Rollback(); err != nil { t.Fatal(err) }
					// Use an independent borrower after cancellation; never rely on
					// a pq transport interrupted by a client deadline remaining live.
					intarrayBoth(t,p,b,observer,intarraySliceStoredCheck(b))
					intarrayBoth(t,p,b,observer,intarraySelect(b,"SELECT id,a FROM ia_slice_rows ORDER BY id",[]string{"id","a"},[]string{"INT4","_INT4"},[]bool{false,true},[][]any{{int64(1),intarraySliceA},{int64(2),"{}"},{int64(3),nil}}))
					intarrayBoth(t,p,b,observer,intarraySliceResult(b,"SELECT ia_provider.idx('{11,22,33,22}'::integer[],22) AS v","INT4",int64(2)))
				})
			})
		}
	}
}
