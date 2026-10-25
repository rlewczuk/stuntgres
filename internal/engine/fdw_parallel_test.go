package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
)

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
