package storage

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

func TestVacuumInspectExecutionPause(t *testing.T) {
	for _, n := range []int{0, 1, 128, 129} {
		for _, callback := range []bool{false, true} {
			t.Run(fmt.Sprintf("rows%d_callback%v", n, callback), func(t *testing.T) {
				rows := make([]RowVersion, n)
				ends := &DeletionRoot{Rows: make(map[ids.RowID]RowEnd)}
				for i := range rows {
					rows[i] = RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Born: 1, InsertFullXID: 3}
					if i%2 == 0 {
						ends.Rows[rows[i].Row] = RowEnd{Version: rows[i].Version, End: 2}
					}
				}
				old, oldEnds := slices.Clone(rows), maps.Clone(ends.Rows)
				pause := util.NewExecutionPause("vacuum.inspect")
				ctx, cancel := context.WithCancel(util.ActivateExecutionPause(util.WithExecutionPause(context.Background(), pause)))
				done := make(chan error, 1)
				joined := false
				unblock := make(chan struct{})
				var exited atomic.Bool
				entered := make(chan int, 2)
				released := false
				t.Cleanup(func() {
					cancel()
					pause.Resume()
					if !released {
						close(unblock)
					}
					if !joined {
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("inspection did not join")
						}
					}
				})
				var progress func(int, int64)
				if callback {
					progress = func(prefix int, removed int64) {
						entered <- prefix
						if prefix == min(n, 128) {
							<-unblock
							exited.Store(true)
						}
					}
				}
				go func() { _, err := InspectVacuum(ctx, rows, ends, 2, 4, true, progress); done <- err }()
				if n == 0 {
					if err := <-done; err != nil {
						t.Fatal(err)
					}
					joined = true
					assertInspectNotAdmitted(t, pause)
					return
				}
				if callback {
					select {
					case prefix := <-entered:
						if prefix != min(n, 128) {
							t.Fatal(prefix)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("callback absent")
					}
					assertInspectNotAdmitted(t, pause)
					close(unblock)
					released = true
				}
				wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if err := pause.Wait(wait); err != nil {
					t.Fatal(err)
				}
				if callback && !exited.Load() {
					t.Fatal("checkpoint preceded callback return")
				}
				cancel()
				select {
				case err := <-done:
					joined = true
					if err != context.Canceled {
						t.Fatal(err)
					}
				case <-wait.Done():
					t.Fatal("cancellation did not release inspection")
				}
				if !reflect.DeepEqual(rows, old) || !reflect.DeepEqual(ends.Rows, oldEnds) {
					t.Fatal("inspection mutated caller roots")
				}
			})
		}
	}
	for _, mode := range []string{"inert", "wrong", "nil", "replacement", "early", "absent"} {
		t.Run(mode, func(t *testing.T) {
			outer := util.NewExecutionPause("vacuum.inspect")
			ctx := util.WithExecutionPause(context.Background(), outer)
			switch mode {
			case "wrong":
				ctx = util.WithExecutionPause(ctx, util.NewExecutionPause("other"))
			case "nil":
				ctx = util.WithExecutionPause(ctx, nil)
			case "replacement":
				inner := util.NewExecutionPause("vacuum.inspect")
				inner.Resume()
				ctx = util.WithExecutionPause(ctx, inner)
			case "early":
				outer.Resume()
				outer.Resume()
			case "absent":
				ctx = context.Background()
			}
			if mode != "inert" {
				ctx = util.ActivateExecutionPause(ctx)
			}
			if _, err := InspectVacuum(ctx, []RowVersion{{Born: 1, InsertFullXID: 3}}, nil, 1, 4, false, nil); err != nil {
				t.Fatal(err)
			}
			if mode == "early" {
				if err := outer.Wait(context.Background()); err == nil || err.Error() != "stuntgres: execution pause resumed before admission" {
					t.Fatal(err)
				}
			} else {
				assertInspectNotAdmitted(t, outer)
			}
		})
	}
	t.Run("replacement_release_and_final_prefix", func(t *testing.T) {
		outer := util.NewExecutionPause("vacuum.inspect")
		inner := util.NewExecutionPause("vacuum.inspect")
		other := util.NewExecutionPause("vacuum.inspect")
		ctx := util.ActivateExecutionPause(util.WithExecutionPause(util.WithExecutionPause(context.Background(), outer), inner))
		otherCtx := util.ActivateExecutionPause(util.WithExecutionPause(context.Background(), other))
		ctx, cancel := context.WithCancel(ctx)
		otherCtx, cancelOther := context.WithCancel(otherCtx)
		done, doneOther := make(chan error, 1), make(chan error, 1)
		joined, joinedOther := false, false
		t.Cleanup(func() {
			cancel()
			cancelOther()
			inner.Resume()
			other.Resume()
			if !joined {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("replacement worker did not join")
				}
			}
			if !joinedOther {
				select {
				case <-doneOther:
				case <-time.After(5 * time.Second):
					t.Error("independent worker did not join")
				}
			}
		})
		var prefixes []int
		go func() {
			_, err := InspectVacuum(ctx, make([]RowVersion, 129), nil, 1, 4, false, func(prefix int, _ int64) { prefixes = append(prefixes, prefix) })
			done <- err
		}()
		go func() {
			_, err := InspectVacuum(otherCtx, []RowVersion{{Born: 1}}, nil, 1, 4, false, nil)
			doneOther <- err
		}()
		wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := inner.Wait(wait); err != nil {
			t.Fatal(err)
		}
		if err := other.Wait(wait); err != nil {
			t.Fatal(err)
		}
		assertInspectNotAdmitted(t, outer)
		inner.Resume()
		inner.Resume()
		select {
		case err := <-done:
			joined = true
			if err != nil {
				t.Fatal(err)
			}
		case <-wait.Done():
			t.Fatal("release did not finish inspection")
		}
		if !reflect.DeepEqual(prefixes, []int{128, 129}) {
			t.Fatal("wrong completed prefixes", prefixes)
		}
		select {
		case err := <-doneOther:
			joinedOther = true
			t.Fatal("replacement release contaminated independent hold", err)
		default:
		}
		if err := other.Wait(wait); err != nil {
			t.Fatal(err)
		}
		cancelOther()
		select {
		case err := <-doneOther:
			joinedOther = true
			if err != context.Canceled {
				t.Fatal(err)
			}
		case <-wait.Done():
			t.Fatal("independent cancellation did not unwind")
		}
		assertInspectNotAdmitted(t, outer)
	})
	t.Run("disabled_allocations", func(t *testing.T) {
		// Measure the disabled inspection itself, not process-global goroutine
		// counts: unrelated runtime/test cleanup can change those counts.
		// The no-goroutine contract also requires review of the synchronous
		// inspection/checkpoint path; allocation counts are not a goroutine census.
		rows := make([]RowVersion, 129)
		for _, ctx := range []context.Context{
			context.Background(),
			util.WithExecutionPause(context.Background(), util.NewExecutionPause("vacuum.inspect")),
			util.ActivateExecutionPause(util.WithExecutionPause(context.Background(), nil)),
		} {
			if allocs := testing.AllocsPerRun(100, func() {
				if _, err := InspectVacuum(ctx, rows, nil, 1, 4, false, nil); err != nil {
					panic(err)
				}
			}); allocs != 0 {
				t.Fatal("disabled inspection allocations", allocs)
			}
		}
	})
}

func assertInspectNotAdmitted(t *testing.T, pause *util.ExecutionPause) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pause.Wait(ctx); err != context.Canceled {
		t.Fatal("false inspection admission", err)
	}
}
