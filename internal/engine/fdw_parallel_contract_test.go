package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type fdwContractWrapper struct {
	fdwDispatchWrapper
	count                                int
	unsafe                               bool
	hook                                 func(context.Context, string, int) error
	begins, ends, opened, closed, serial int
}

func (w *fdwContractWrapper) event(ctx context.Context, phase string, index int) error {
	if w.hook != nil {
		return w.hook(ctx, phase, index)
	}
	return ctx.Err()
}
func (w *fdwContractWrapper) IsForeignScanParallelSafe(ctx context.Context, _ catalog.ForeignRequest, _ catalog.ForeignPlan) (bool, error) {
	return !w.unsafe, w.event(ctx, "Safe", -1)
}
func (w *fdwContractWrapper) BeginForeignScan(ctx context.Context, r catalog.ForeignRequest, p catalog.ForeignPlan) (catalog.ForeignScan, error) {
	w.serial++
	return w.fdwDispatchWrapper.BeginForeignScan(ctx, r, p)
}
func (w *fdwContractWrapper) BeginForeignParallelScan(ctx context.Context, _ catalog.ForeignRequest, _ catalog.ForeignPlan, _ int) (catalog.ForeignParallelScan, error) {
	w.begins++
	return w, w.event(ctx, "Begin", -1)
}
func (w *fdwContractWrapper) PartitionCount() int { return w.count }
func (w *fdwContractWrapper) BeginPartitionForeignScan(ctx context.Context, index int) (catalog.ForeignScan, error) {
	w.opened++
	s := &fdwContractScan{owner: w, index: index, rows: w.rows[len(w.rows)*index/w.count : len(w.rows)*(index+1)/w.count]}
	return s, w.event(ctx, "Partition", index)
}
func (w *fdwContractWrapper) EndForeignParallelScan(ctx context.Context) error {
	w.ends++
	return w.event(ctx, "EndShared", -1)
}

type fdwContractScan struct {
	owner       *fdwContractWrapper
	index, next int
	rows        []catalog.ForeignRow
}

func (s *fdwContractScan) IterateForeignScan(ctx context.Context) (catalog.ForeignRow, bool, error) {
	if err := s.owner.event(ctx, "Iterate", s.index); err != nil {
		return nil, false, err
	}
	if s.next == len(s.rows) {
		return nil, false, nil
	}
	row := s.rows[s.next]
	s.next++
	return row, true, nil
}
func (*fdwContractScan) ReScanForeignScan(context.Context, catalog.ForeignScanParameters) error {
	return errors.New("unexpected partition rescan")
}
func (s *fdwContractScan) EndForeignScan(ctx context.Context) error {
	s.owner.closed++
	return s.owner.event(ctx, "End", s.index)
}

func fdwContractFixture(t *testing.T, q string) (*plan.Query, *exec.Source, *fdwContractWrapper) {
	t.Helper()
	_, query, captured := queryCoreFixture(t, q)
	w := &fdwContractWrapper{count: 2}
	for _, version := range captured.Committed {
		id, _ := version.Values[0].Integer()
		w.rows = append(w.rows, catalog.ForeignRow{id})
	}
	column := query.Root.Schema()[0]
	f := &exec.ForeignQuerySource{Wrapper: w, Request: catalog.ForeignRequest{Columns: []catalog.ForeignColumn{{Attribute: 1, Name: "id"}}, RequiredAttributes: []int32{1}}, Columns: []catalog.Column{{Name: "id", Usage: column.Usage}}, ImportValue: func(_ context.Context, v driver.Value, u sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
		return sqltype.Integer(u.Type, v.(int64))
	}}
	owner := &exec.ForeignScanOwner{}
	owner.Add(f)
	return query, &exec.Source{Ref: captured.Ref, Anonymous: [][]sqltype.Value{}, Foreign: f}, w
}
func fdwContractBalanced(t *testing.T, w *fdwContractWrapper) {
	t.Helper()
	if w.begins != w.ends || w.opened != w.closed {
		t.Fatal("ownership mismatch", w.begins, w.ends, w.opened, w.closed)
	}
}

func TestFDWParallelPartition(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 7, 13} {
		for _, parts := range []int{1, 2} {
			query, source, w := fdwContractFixture(t, "SELECT id FROM t ORDER BY id")
			w.count = parts
			w.rows = nil
			var want []int64
			for i := range n {
				id := int64(i / 2)
				w.rows = append(w.rows, catalog.ForeignRow{id})
				want = append(want, id)
			}
			rows, count, err := exec.FinalizeSelect(context.Background(), query.Root, source, nil, 2)
			if err != nil || count != n {
				t.Fatal(n, parts, count, err)
			}
			if got := parallelTestIDs(t, rows); !reflect.DeepEqual(got, want) {
				t.Fatal(got, want)
			}
			fdwContractBalanced(t, w)
		}
	}
}
func TestFDWParallelOwnership(t *testing.T) {
	for _, phase := range []string{"Begin", "Partition", "PartitionLater", "End", "EndShared"} {
		query, source, w := fdwContractFixture(t, "SELECT id FROM t ORDER BY id")
		failure := errors.New(phase)
		var partitionEnds [2]int
		w.hook = func(ctx context.Context, at string, index int) error {
			if phase == "PartitionLater" {
				if at == "Iterate" {
					t.Error("started workers before all partitions were acquired")
				}
				if at == "End" {
					partitionEnds[index]++
				}
				if at == "EndShared" && partitionEnds != [2]int{1, 1} {
					t.Error("shared End preceded exact-once partition cleanup", partitionEnds)
				}
			}
			if at == phase || phase == "PartitionLater" && at == "Partition" && index == 1 {
				return failure
			}
			return ctx.Err()
		}
		rows, count, err := exec.FinalizeSelect(context.Background(), query.Root, source, nil, 2)
		if rows != nil || count != 0 || err != failure {
			t.Fatal(phase, rows, count, err)
		}
		fdwContractBalanced(t, w)
		if phase == "PartitionLater" && (w.opened != 2 || w.closed != 2 || partitionEnds != [2]int{1, 1}) {
			t.Fatal("later nonnil/error acquisition did not dispose both partitions", w.opened, w.closed, partitionEnds)
		}
	}
	for _, n := range []int{0, 3} {
		query, source, w := fdwContractFixture(t, "SELECT id FROM t ORDER BY id")
		w.count = n
		rows, count, err := exec.FinalizeSelect(context.Background(), query.Root, source, nil, 2)
		if rows != nil || count != 0 || err == nil {
			t.Fatal("invalid partition count accepted", n)
		}
		fdwContractBalanced(t, w)
	}
	for _, width := range []int{0, 2} {
		query, source, w := fdwContractFixture(t, "SELECT id FROM t ORDER BY id")
		w.rows = []catalog.ForeignRow{make(catalog.ForeignRow, width)}
		rows, count, err := exec.FinalizeSelect(context.Background(), query.Root, source, nil, 2)
		if rows != nil || count != 0 || err == nil || err.Error() != "stuntgres: foreign scan row width does not match its plan" {
			t.Fatal("invalid width published", width, rows, count, err)
		}
		fdwContractBalanced(t, w)
	}
}
func TestFDWParallelEligibility(t *testing.T) {
	for _, tc := range []struct {
		q               string
		ceiling         int
		unsafe, generic bool
	}{
		{"SELECT id FROM t ORDER BY id", 0, false, false}, {"SELECT id FROM t", 1, false, false},
		{"SELECT id FROM t", 2, true, false}, {"SELECT id FROM t", 2, false, true},
		{"SELECT id FROM t LIMIT 1", 2, false, false}, {"SELECT id FROM t OFFSET 1", 2, false, false},
	} {
		query, source, w := fdwContractFixture(t, tc.q)
		w.unsafe = tc.unsafe
		var rows exec.RowResult
		var err error
		if tc.generic {
			rows, _, err = exec.Finalize(context.Background(), query.Root, source, nil)
		} else {
			rows, _, err = exec.FinalizeSelect(context.Background(), query.Root, source, nil, tc.ceiling)
		}
		if err != nil {
			t.Fatal(tc, err)
		}
		rows.Close()
		if w.begins != 0 || w.serial != 1 {
			t.Fatal("ineligible execution did not use ordinary scan", tc, w.begins, w.serial)
		}
	}
}
func TestFDWParallelCancellation(t *testing.T) {
	t.Run("backpressured_cancel", func(t *testing.T) { fdwParallelPressure(t, false) })
	t.Run("backpressured_import_failure", func(t *testing.T) { fdwParallelPressure(t, true) })
	query, source, w := fdwContractFixture(t, "SELECT id FROM t ORDER BY id")
	entered := make(chan int, 2)
	w.hook = func(ctx context.Context, phase string, index int) error {
		if phase == "Iterate" {
			entered <- index
			<-ctx.Done()
			return ctx.Err()
		}
		return ctx.Err()
	}
	ctx, cancel, join := parallelTestStart(t, func(ctx context.Context) parallelTestOutcome {
		r, n, e := exec.FinalizeSelect(ctx, query.Root, source, nil, 2)
		return parallelTestOutcome{r, n, e}
	})
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	cancel()
	out := join()
	if out.rows != nil || out.count != 0 || !errors.Is(out.err, context.Canceled) {
		t.Fatal(out)
	}
	fdwContractBalanced(t, w)
}
func TestFDWParallelErrorIdentity(t *testing.T) {
	query, source, w := fdwContractFixture(t, "SELECT id FROM t ORDER BY id")
	primary, secondary := errors.New("primary"), errors.New("cleanup")
	w.hook = func(ctx context.Context, phase string, index int) error {
		if phase == "Iterate" {
			if index == 0 {
				return primary
			}
			<-ctx.Done()
			return ctx.Err()
		}
		if phase == "End" || phase == "EndShared" {
			if ctx.Err() != nil {
				return errors.New("cleanup canceled")
			}
			return secondary
		}
		return ctx.Err()
	}
	rows, count, err := exec.FinalizeSelect(context.Background(), query.Root, source, nil, 2)
	if rows != nil || count != 0 || err != primary {
		t.Fatal(rows, count, err)
	}
	fdwContractBalanced(t, w)
}
