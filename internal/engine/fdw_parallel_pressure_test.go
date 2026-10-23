package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

// The first partition owns all four captured occurrences; the second is empty
// but keeps an actual Iterate callback alive through cancellation. An empty
// partition is legal and makes queue occupancy independent of worker ordering.
type fdwPressureWrapper struct {
	*fdwContractWrapper
}

func (w *fdwPressureWrapper) BeginForeignParallelScan(ctx context.Context, _ catalog.ForeignRequest, _ catalog.ForeignPlan, _ int) (catalog.ForeignParallelScan, error) {
	w.begins++
	return w, w.event(ctx, "Begin", -1)
}

func (w *fdwPressureWrapper) BeginPartitionForeignScan(ctx context.Context, index int) (catalog.ForeignScan, error) {
	w.opened++
	var rows []catalog.ForeignRow
	if index == 0 {
		rows = w.rows
	}
	return &fdwContractScan{owner: w.fdwContractWrapper, index: index, rows: rows}, w.event(ctx, "Partition", index)
}

func fdwParallelPressure(t *testing.T, importFailure bool) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		query, source, w := fdwContractFixture(t, "SELECT id FROM t ORDER BY id")
		source.Foreign.Wrapper = &fdwPressureWrapper{w}
		owner := &exec.ForeignScanOwner{}
		owner.Add(source.Foreign)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		delivered := make(chan struct{})
		releaseImport := make(chan struct{})
		workerCanceled := make(chan struct{})
		releaseWorker := make(chan struct{})
		workerExited := make(chan struct{})
		var calls, ended [2]int
		sharedEnds := 0
		w.hook = func(ctx context.Context, phase string, index int) error {
			switch phase {
			case "Iterate":
				calls[index]++
				if index == 1 {
					<-ctx.Done()
					close(workerCanceled)
					// Deliberately delay the provider's return after it has
					// observed cancellation, so premature disposal is visible.
					<-releaseWorker
					close(workerExited)
					return ctx.Err()
				}
			case "End", "EndShared":
				if ctx.Err() != nil {
					t.Error("cleanup did not detach cancellation", ctx.Err())
				}
				select {
				case <-workerExited:
				default:
					t.Error("disposed provider resources before worker callback exit")
				}
				if phase == "End" {
					ended[index]++
				} else {
					sharedEnds++
					if ended != [2]int{1, 1} {
						t.Error("shared End preceded partition Ends", ended)
					}
				}
				return nil
			}
			return ctx.Err()
		}
		failure := errors.New("leader import failed after delivery")
		source.Foreign.ImportValue = func(ctx context.Context, _ driver.Value, _ sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
			// ImportValues is called only after the production adapter has
			// actually delivered a row to its sole consumer.
			close(delivered)
			<-releaseImport
			if importFailure {
				return sqltype.Value{}, failure
			}
			return sqltype.Value{}, ctx.Err()
		}
		finished := make(chan parallelTestOutcome, 1)
		go func() {
			r, n, err := exec.FinalizeSelect(ctx, query.Root, source, nil, 2)
			finished <- parallelTestOutcome{r, n, err}
		}()
		<-delivered
		synctest.Wait()
		// Wait settles all bubble goroutines at durable blocking points.
		// Exactly four producer callbacks mean: one delivered row, two
		// queued rows (the actual two-partition channel capacity), and the
		// fourth row blocked in the real worker send. A fifth callback
		// would mean the transport was not bounded as promised. There are
		// no sleeps, queue replicas or production test hooks in this proof.
		if calls != [2]int{4, 1} {
			t.Error("producer did not settle at the bounded transfer", calls)
		}
		if ended != [2]int{} || sharedEnds != 0 {
			t.Error("resources ended while consumer was held", ended, sharedEnds)
		}
		if !importFailure {
			cancel()
		}
		close(releaseImport)
		<-workerCanceled
		synctest.Wait()
		// The leader must join, not dispose, while this started callback
		// remains alive. Its sibling is blocked in row transfer, not I/O.
		if ended != [2]int{} || sharedEnds != 0 {
			t.Error("cleanup did not wait for all workers", ended, sharedEnds)
		}
		select {
		case out := <-finished:
			t.Error("execution returned before worker exit", out)
		default:
		}
		close(releaseWorker)
		synctest.Wait()
		select {
		case out := <-finished:
			want := error(context.Canceled)
			if importFailure {
				want = failure
			}
			if out.rows != nil || out.count != 0 || out.err != want {
				t.Error("failure published rows or lost primary identity", out)
			}
		default:
			t.Fatal("canceled backpressured producer did not join")
		}
		if ended != [2]int{1, 1} || sharedEnds != 1 {
			t.Error("resources not ended exactly once", ended, sharedEnds)
		}
		fdwContractBalanced(t, w)

		// Reuse the same lifetime owner with a fresh captured execution;
		// a finished source itself is intentionally not reusable.
		recoveryQuery, recoverySource, recovery := fdwContractFixture(t, "SELECT id FROM t ORDER BY id")
		owner.Add(recoverySource.Foreign)
		rows, count, err := exec.FinalizeSelect(context.Background(), recoveryQuery.Root, recoverySource, nil, 2)
		if err != nil || count != 4 {
			t.Fatal("same-owner recovery failed", count, err)
		}
		if got := parallelTestIDs(t, rows); !reflect.DeepEqual(got, []int64{1, 2, 3, 4}) {
			t.Error("same-owner recovery changed occurrences", got)
		}
		if err := owner.Finish(context.Background()); err != nil {
			t.Error("owner finish after recovery", err)
		}
		fdwContractBalanced(t, recovery)
		if ended != [2]int{1, 1} || sharedEnds != 1 {
			t.Error("owner finish repeated failed-source cleanup", ended, sharedEnds)
		}
	})
}
