package reference_test

import (
	"context"
	"errors"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func referenceRoutingConcurrentExec(ctx context.Context, x fixture.Executor, query string, done chan<- fixture.RelationOutcome) {
	result, err := x.ExecContext(ctx, query)
	done <- fixture.RelationOutcome{Result: result, Err: err}
}

// Match the live provider and this worker in one goroutine, never creation
// metadata or an unrelated session. PostgreSQL instead observes the exact blocker.
func referenceRoutingNativeRowWait(t *testing.T, parent context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	buffer := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buffer, true)
		if n == len(buffer) {
			buffer = make([]byte, 2*len(buffer))
			continue
		}
		for _, stack := range strings.Split(string(buffer[:n]), "\n\n") {
			live := strings.Split(stack, "\ncreated by ")[0]
			if strings.Contains(live, "referenceRoutingConcurrentExec(") && strings.Contains(live, ".AcquireUpdateRow(") {
				return
			}
		}
		if ctx.Err() != nil {
			t.Fatal("routing worker did not enter row arbitration", ctx.Err())
		}
		runtime.Gosched()
	}
}

func TestReferencePartitionRoutingConcurrency(t *testing.T) {
	for _, lane := range []string{"cancel", "move_commit", "move_rollback", "predicate_recheck", "latest"} {
		t.Run(lane, func(t *testing.T) {
			referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				owner, worker := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE pr_con(id INT,k INT,visits INT) PARTITION BY RANGE(k)", 0))
				run(fixture.Exec("CREATE TABLE pr_con_low PARTITION OF pr_con FOR VALUES FROM(1) TO(3)", 0))
				run(fixture.Exec("CREATE TABLE pr_con_high PARTITION OF pr_con FOR VALUES FROM(3) TO(5)", 0))
				run(fixture.Exec("CREATE UNIQUE INDEX pr_con_unique ON pr_con_high(id)", 0))
				run(fixture.Exec("INSERT INTO pr_con VALUES(10,2,0)", 1))
				query := "UPDATE pr_con SET k=3,visits=visits+1 WHERE id=10"
				if lane == "predicate_recheck" {
					query += " AND visits=0"
				}
				stmt, err := worker.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: worker, Statement: stmt, SQL: query}
				var ownerPID, workerPID int
				if b.PG {
					ownerPID, workerPID = fixture.RowPIDs(t, p.Ctx, owner, worker)
				}
				tx, err := owner.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				hold := "UPDATE pr_con SET visits=5 WHERE id=10"
				if lane == "move_commit" || lane == "move_rollback" {
					hold = "UPDATE pr_con SET k=4,visits=5 WHERE id=10"
				}
				fixture.LockExec(t, p.Ctx, tx, hold, 1)
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				done := make(chan fixture.RelationOutcome, 1)
				go referenceRoutingConcurrentExec(ctx, held, query, done)
				joined := false
				defer func() {
					cancel()
					if !joined {
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("routing worker did not join")
						}
					}
				}()
				if b.PG {
					fixture.RowWait(t, p.Ctx, workerPID, ownerPID)
				} else {
					referenceRoutingNativeRowWait(t, p.Ctx)
				}
				if lane == "cancel" {
					if b.PG {
						var acknowledged bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&acknowledged); err != nil || !acknowledged {
							t.Fatal("cancel acknowledgement", acknowledged, err)
						}
					} else {
						cancel()
					}
				} else if lane == "move_rollback" {
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
				}
				out := fixture.LockResult(t, done)
				joined = true
				wantK, wantVisits := int64(3), int64(6)
				switch lane {
				case "cancel":
					if out.Result != nil {
						t.Fatal("canceled UPDATE exposed a result")
					}
					if b.PG {
						got := fixture.ExtractDiagnostic(t, out.Err)
						want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						if !regexp.MustCompile(`^while deleting tuple \([0-9]+,[1-9][0-9]*\) in relation "pr_con_low"$`).MatchString(got.Context) {
							t.Fatalf("cancel Context %q", got.Context)
						}
						want.Context = got.Context
						fixture.AssertDiagnostic(t, out.Err, &want)
					} else if !errors.Is(out.Err, context.Canceled) {
						t.Fatal(out.Err)
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					wantK, wantVisits = 2, 0
				case "move_commit":
					if out.Result != nil {
						t.Fatal("concurrent movement exposed a result")
					}
					want := fixture.ControlsReferenceDiagnostic("40001", "tuple to be locked was already moved to another partition due to concurrent update")
					fixture.AssertDiagnostic(t, out.Err, &want)
					wantK, wantVisits = 4, 5
				case "move_rollback":
					if out.Err != nil {
						t.Fatal(out.Err)
					}
					fixture.LockAffected(t, out.Result, 1)
					wantVisits = 1
				case "predicate_recheck":
					if out.Err != nil {
						t.Fatal(out.Err)
					}
					fixture.LockAffected(t, out.Result, 0)
					wantK, wantVisits = 2, 5
				case "latest":
					if out.Err != nil {
						t.Fatal(out.Err)
					}
					fixture.LockAffected(t, out.Result, 1)
				}
				// Use the same worker owner after cancellation/error; no closed-pool substitute.
				s := fixture.Select("SELECT id,k,visits FROM pr_con", []string{"id", "k", "visits"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), wantK, wantVisits}})
				fixture.RunStep(t, p.Ctx, worker, b.Recorder, s)
				for _, leaf := range []string{"pr_con_low", "pr_con_high"} {
					physical := s
					physical.Query = "SELECT id,k,visits FROM " + leaf
					if (wantK < 3) != (leaf == "pr_con_low") {
						physical.Rows = nil
					}
					fixture.RunStep(t, p.Ctx, worker, b.Recorder, physical)
				}
				if lane == "cancel" {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1))
				}
			})
		})
	}
}
