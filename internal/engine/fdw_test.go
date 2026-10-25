package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
)

type fdwUnitWrapper struct {
	paths    []catalog.ForeignPath
	chosen   catalog.ForeignPath
	scan     *fdwUnitScan
	beginErr error
}

func (*fdwUnitWrapper) GetForeignRelSize(context.Context, catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	return catalog.ForeignRelSize{Rows: 3}, nil
}

func (w *fdwUnitWrapper) GetForeignPaths(context.Context, catalog.ForeignRequest, catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	return w.paths, nil
}

func (w *fdwUnitWrapper) GetForeignPlan(_ context.Context, _ catalog.ForeignRequest, path catalog.ForeignPath) (catalog.ForeignPlan, error) {
	w.chosen = path
	return catalog.ForeignPlan{Attributes: path.Attributes, Private: path.Private}, nil
}

func (w *fdwUnitWrapper) BeginForeignScan(context.Context, catalog.ForeignRequest, catalog.ForeignPlan) (catalog.ForeignScan, error) {
	return w.scan, w.beginErr
}

type fdwUnitScan struct {
	position, rescans, ends int
	rescanErr, endErr       error
	cleanupCanceled         bool
	onEnd                   func(context.Context) error
}

func (s *fdwUnitScan) IterateForeignScan(context.Context) (catalog.ForeignRow, bool, error) {
	if s.position == 3 {
		return nil, false, nil
	}
	s.position++
	return catalog.ForeignRow{}, true, nil
}

func (s *fdwUnitScan) ReScanForeignScan(context.Context, catalog.ForeignScanParameters) error {
	s.rescans++
	s.position = 0
	return s.rescanErr
}

func (s *fdwUnitScan) EndForeignScan(ctx context.Context) error {
	s.ends++
	s.cleanupCanceled = ctx.Err() != nil
	if s.onEnd != nil {
		return s.onEnd(ctx)
	}
	return s.endErr
}
