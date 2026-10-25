package engine

import (
	"context"
	"database/sql/driver"
	"errors"
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
