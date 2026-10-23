package engine

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

// Exercises the production SELECT dispatcher, not a coordinator-only helper.
// Inputs are real captured engine rows, with the ordinary scan available so a
// missing dispatch produces a bounded, deterministic failing gate assertion.
func TestFDWParallelConcurrentExecution(t *testing.T) {
	_, query, captured := queryCoreFixture(t, "SELECT id FROM t ORDER BY id")
	entered := make(chan int, 2)
	release := make(chan struct{})
	w := &fdwDispatchWrapper{entered: entered, release: release}
	for _, version := range captured.Committed {
		id, err := version.Values[0].Integer()
		if err != nil {
			t.Fatal(err)
		}
		w.rows = append(w.rows, catalog.ForeignRow{id})
	}
	columns := query.Root.Schema()
	f := &exec.ForeignQuerySource{Wrapper: w, Request: catalog.ForeignRequest{Columns: []catalog.ForeignColumn{{Attribute: 1, Name: "id"}}, RequiredAttributes: []int32{1}}, Columns: []catalog.Column{{Name: "id", Usage: columns[0].Usage}}, ImportValue: func(_ context.Context, value driver.Value, usage sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
		return sqltype.Integer(usage.Type, value.(int64))
	}}
	owner := &exec.ForeignScanOwner{}
	owner.Add(f)
	source := &exec.Source{Ref: captured.Ref, Anonymous: [][]sqltype.Value{}, Foreign: f}
	ctx, _, join := parallelTestStart(t, func(ctx context.Context) parallelTestOutcome {
		r, n, e := exec.FinalizeSelect(ctx, query.Root, source, nil, 2)
		return parallelTestOutcome{r, n, e}
	})
	seen := map[int]bool{}
	for range 2 {
		select {
		case index := <-entered:
			if seen[index] {
				t.Fatal("same partition entered twice", index)
			}
			seen[index] = true
		case <-ctx.Done():
			t.Fatal("production dispatch did not consume two distinct partitions", ctx.Err())
		}
	}
	close(release)
	out := join()
	if out.err != nil || out.count != 4 {
		t.Fatal(out.count, out.err)
	}
	if got := parallelTestIDs(t, out.rows); !reflect.DeepEqual(got, []int64{1, 2, 3, 4}) {
		t.Fatal(got)
	}
}

type fdwDispatchWrapper struct {
	rows    []catalog.ForeignRow
	entered chan int
	release <-chan struct{}
}

func (w *fdwDispatchWrapper) GetForeignRelSize(context.Context, catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	return catalog.ForeignRelSize{Rows: float64(len(w.rows))}, nil
}
func (*fdwDispatchWrapper) GetForeignPaths(context.Context, catalog.ForeignRequest, catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	return []catalog.ForeignPath{{Attributes: []int32{1}}}, nil
}
func (*fdwDispatchWrapper) GetForeignPlan(_ context.Context, _ catalog.ForeignRequest, p catalog.ForeignPath) (catalog.ForeignPlan, error) {
	return catalog.ForeignPlan{Attributes: p.Attributes}, nil
}
func (w *fdwDispatchWrapper) BeginForeignScan(context.Context, catalog.ForeignRequest, catalog.ForeignPlan) (catalog.ForeignScan, error) {
	return &fdwDispatchPartition{rows: w.rows}, nil
}
func (*fdwDispatchWrapper) IsForeignScanParallelSafe(context.Context, catalog.ForeignRequest, catalog.ForeignPlan) (bool, error) {
	return true, nil
}
func (w *fdwDispatchWrapper) BeginForeignParallelScan(context.Context, catalog.ForeignRequest, catalog.ForeignPlan, int) (catalog.ForeignParallelScan, error) {
	return w, nil
}
func (*fdwDispatchWrapper) PartitionCount() int { return 2 }
func (w *fdwDispatchWrapper) BeginPartitionForeignScan(_ context.Context, index int) (catalog.ForeignScan, error) {
	return &fdwDispatchPartition{rows: w.rows[len(w.rows)*index/2 : len(w.rows)*(index+1)/2], index: index, entered: w.entered, release: w.release}, nil
}
func (*fdwDispatchWrapper) EndForeignParallelScan(context.Context) error { return nil }

type fdwDispatchPartition struct {
	rows        []catalog.ForeignRow
	index, next int
	entered     chan int
	release     <-chan struct{}
}

func (s *fdwDispatchPartition) IterateForeignScan(ctx context.Context) (catalog.ForeignRow, bool, error) {
	if s.next == 0 && s.entered != nil {
		select {
		case s.entered <- s.index:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
		s.entered = nil
	}
	if s.next == len(s.rows) {
		return nil, false, nil
	}
	row := s.rows[s.next]
	s.next++
	return row, true, nil
}
func (*fdwDispatchPartition) ReScanForeignScan(context.Context, catalog.ForeignScanParameters) error {
	return fmt.Errorf("unexpected rescan")
}
func (*fdwDispatchPartition) EndForeignScan(context.Context) error { return nil }
