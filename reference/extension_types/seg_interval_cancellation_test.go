package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func segIntervalContextError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("ordinary context error %v want %v", err, want)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("ordinary context error gained SQLSTATE", err)
	}
}
func TestReferenceSegIntervalCancellation(t *testing.T) {
	t.Run("context_boundaries", func(t *testing.T) {
		segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE seg_interval_cancel(id integer,hit boolean)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO seg_interval_cancel VALUES (1,true)", 1))
			if !b.PG {
				canceled, stop := context.WithCancel(p.Ctx)
				stop()
				expired, cancel := context.WithDeadline(p.Ctx, time.Unix(0, 0))
				defer cancel()
				for _, ctx := range []context.Context{canceled, expired} {
					want := context.Canceled
					if ctx == expired {
						want = context.DeadlineExceeded
					}
					_, err := c.PrepareContext(ctx, "SELECT $1::seg && $2::seg")
					segIntervalContextError(t, err, want)
					_, err = c.ExecContext(ctx, "INSERT INTO seg_interval_cancel VALUES (2,$1::seg && $2::seg)", "1", "1")
					segIntervalContextError(t, err, want)
					_, err = c.QueryContext(ctx, "SELECT $1::seg = $2::seg", "1", "1")
					segIntervalContextError(t, err, want)
					var dump bytes.Buffer
					for _, err := range []error{stuntgres.Dump(ctx, p.Native, &dump), stuntgres.Reset(ctx, p.Native), stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE EXTENSION seg;"))} {
						segIntervalContextError(t, err, want)
					}
					<-ctx.Done()
					segIntervalContextError(t, ctx.Err(), want)
				}
				ctx, cancelRows := context.WithCancel(p.Ctx)
				rows, err := c.QueryContext(ctx, "SELECT '1'::seg && '1'::seg AS v")
				if err != nil {
					cancelRows()
					t.Fatal(err)
				}
				cols, err := rows.ColumnTypes()
				if err != nil {
					rows.Close()
					cancelRows()
					t.Fatal(err)
				}
				cancelRows()
				<-ctx.Done()
				if rows.Next() {
					rows.Close()
					t.Fatal("canceled iteration returned predicate")
				}
				segIntervalContextError(t, rows.Err(), context.Canceled)
				rows.Close()
				segIntervalMetadata(t, false, cols, []string{"v"}, []string{"BOOL"}, false)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,hit FROM seg_interval_cancel ORDER BY id", []string{"id", "hit"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
		})
	})
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct_admitted", true: "prepared_admitted"}[prepared], func(t *testing.T) {
			segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE seg_interval_cancel(id integer,hit boolean)", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO seg_interval_cancel VALUES (1,true)", 1))
				holder, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Close()
				observer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
				q := "INSERT INTO seg_interval_cancel VALUES (2,('0 .. 1'::seg && '1 .. 2'::seg)),(3,('1.0 .. 1.00'::seg = '1.0'::seg))"
				var stmt *sql.Stmt
				if prepared {
					stmt, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				workerPID, holderPID := 0, 0
				var oid int64
				if b.PG {
					workerPID = fixture.LockPID(t, p.Ctx, c)
					holderPID = fixture.LockPID(t, p.Ctx, holder)
					oid = fixture.LockOID(t, p.Ctx, "seg_interval_cancel")
				}
				tx, err := holder.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE seg_interval_cancel IN ACCESS EXCLUSIVE MODE", 0))
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				finished := make(chan struct{})
				join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					var result sql.Result
					var err error
					if prepared {
						result, err = stmt.ExecContext(ctx)
					} else {
						result, err = c.ExecContext(ctx, q)
					}
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				fixture.RelationLockPending(t, p, b, workerPID, holderPID, oid, "RowExclusiveLock", finished)
				if b.PG {
					var ok bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&ok); err != nil || !ok {
						t.Fatal("server cancellation", ok, err)
					}
				} else {
					cancel()
				}
				out := join()
				if out.Result != nil || out.Rows != nil {
					t.Fatal("canceled mutation published result")
				}
				if b.PG {
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					fixture.AssertDiagnostic(t, out.Err, &want)
				} else {
					segIntervalContextError(t, out.Err, context.Canceled)
					<-ctx.Done()
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT id,hit FROM seg_interval_cancel ORDER BY id", []string{"id", "hit"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
			})
		})
	}
}
