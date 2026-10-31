package core_test

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
)

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
