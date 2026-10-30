package exec

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type foreignCapturedRow struct {
	identity catalog.ForeignRowIdentity
	request  *catalog.ForeignRowMarkRequest
	latest   []sqltype.Value
	settled  plan.RowMark
	updated  bool
}

// A generation owns one immutable request shared by all of its captured rows.
// Callback requests are copied separately; a rescan replaces, never mutates,
// this snapshot so earlier captures retain their parameters and selected mark.
func (execution *ForeignExecution) snapshotRequest(request catalog.ForeignRequest) {
	if request.RowMark == nil {
		execution.request = CopyForeignRequest(request)
		execution.markRequest = nil
		return
	}
	snapshot := foreignMarkRequest(request, execution.rowMark)
	execution.markRequest = &snapshot
	execution.request = snapshot.ForeignRequest
}

func foreignMarkRequest(request catalog.ForeignRequest, mark plan.RowMark) catalog.ForeignRowMarkRequest {
	r := catalog.ForeignRowMarkRequest{ForeignRequest: CopyForeignRequest(request)}
	switch mark.Mode {
	case lock.ForKeyShare:
		r.Strength = catalog.ForeignLockKeyShare
	case lock.ForShare:
		r.Strength = catalog.ForeignLockShare
	case lock.ForNoKeyUpdate:
		r.Strength = catalog.ForeignLockNoKeyUpdate
	case lock.ForUpdate:
		r.Strength = catalog.ForeignLockUpdate
	}
	switch mark.Wait {
	case parser.RowLockNowait:
		r.WaitPolicy = catalog.ForeignLockNoWait
	case parser.RowLockSkip:
		r.WaitPolicy = catalog.ForeignLockSkipLocked
	}
	return r
}

func validateForeignMark(wrapper catalog.ForeignDataWrapper, request catalog.ForeignRowMarkRequest, selected catalog.ForeignRowMarkPlan) error {
	if selected.Type > catalog.ForeignRowMarkUpdate {
		return fmt.Errorf("stuntgres: invalid foreign row mark type")
	}
	if selected.Type >= catalog.ForeignRowMarkKeyShare && (request.Strength == catalog.ForeignLockNone || int(selected.Type)-1 < int(request.Strength)) {
		return fmt.Errorf("stuntgres: foreign row mark weakens the requested lock strength")
	}
	if selected.Type == catalog.ForeignRowMarkCopy {
		return nil
	}
	if len(selected.Identity.Names) == 0 {
		return fmt.Errorf("stuntgres: foreign row mark requires an identity descriptor")
	}
	for i, name := range selected.Identity.Names {
		if name == "" || slices.Contains(selected.Identity.Names[:i], name) {
			return fmt.Errorf("stuntgres: invalid foreign row identity descriptor")
		}
	}
	if _, ok := wrapper.(catalog.ForeignRowRefetcher); !ok {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "foreign row marking requires a refetch callback"}
	}
	if selected.Type >= catalog.ForeignRowMarkKeyShare {
		if _, ok := wrapper.(catalog.ForeignTransactionProvider); !ok {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "foreign row locking requires transaction support"}
		}
	}
	return nil
}
func (f *ForeignQuerySource) selectRowMark(ctx context.Context, request *catalog.ForeignRequest, mark plan.RowMark) error {
	selected := catalog.ForeignRowMarkPlan{}
	if provider, ok := f.Wrapper.(catalog.ForeignRowMarkProvider); ok {
		var err error
		selected, err = provider.GetForeignRowMarkType(ctx, foreignMarkRequest(*request, mark))
		if err != nil {
			return ForeignCallbackError(err)
		}
	}
	if err := validateForeignMark(f.Wrapper, foreignMarkRequest(*request, mark), selected); err != nil {
		return err
	}
	selected.Identity.Names = slices.Clone(selected.Identity.Names)
	request.RowMark = &selected
	if selected.Type != catalog.ForeignRowMarkCopy {
		request.Identity = &selected.Identity
	}
	// EPQ and public refetch both require the complete declared tuple.
	request.RequiredAttributes = make([]int32, len(request.Columns))
	for i, column := range request.Columns {
		request.RequiredAttributes[i] = column.Attribute
	}
	return nil
}

func validateForeignIdentity(identity catalog.ForeignRowIdentity, descriptor *catalog.ForeignUpdateTargets) error {
	if descriptor == nil || len(identity) != len(descriptor.Names) {
		return fmt.Errorf("stuntgres: foreign row identity width does not match its descriptor")
	}
	for _, cell := range identity {
		if !driver.IsValue(cell) {
			return fmt.Errorf("stuntgres: foreign row identity contains a non-driver value")
		}
		if _, bytes := cell.([]byte); !bytes && cell != nil && !reflect.TypeOf(cell).Comparable() {
			return fmt.Errorf("stuntgres: foreign row identity contains a noncomparable driver value")
		}
	}
	return nil
}

func (f *ForeignQuerySource) refetchRow(ctx context.Context, source *Source, visible int, mark plan.RowMark, reference bool) ([]sqltype.Value, bool, bool, error) {
	if visible < 0 || visible >= len(f.captured) {
		return nil, false, false, fmt.Errorf("stuntgres: missing foreign row capture")
	}
	captured := &f.captured[visible]
	request := *captured.request
	selected := request.RowMark
	row := source.Row(visible)
	if captured.latest != nil {
		row = captured.latest
	}
	if selected.Type == catalog.ForeignRowMarkCopy || selected.Type == catalog.ForeignRowMarkReference && !reference {
		return source.Row(visible), true, false, nil
	}
	if selected.Type >= catalog.ForeignRowMarkKeyShare && (mark.Mode == 0 || captured.settled.Mode != 0 && MergeQueryRowMark(captured.settled, mark) == captured.settled) {
		return row, true, captured.updated && len(row) != 0, nil
	}
	request = foreignMarkRequest(request.ForeignRequest, mark)
	result, err := f.Wrapper.(catalog.ForeignRowRefetcher).RefetchForeignRow(ctx, request, CopyForeignCells(captured.identity))
	if err != nil {
		return nil, false, false, ForeignCallbackError(err)
	}
	if !result.Present {
		return nil, false, true, nil
	}
	if len(result.Row) != len(f.Columns) {
		return nil, false, false, fmt.Errorf("stuntgres: foreign refetch row width does not match its declaration")
	}
	input, output := source.LiveHost().Environments()
	values, err := f.ImportValues(ctx, result.Row, nil, input, output, nil, nil)
	if err != nil {
		return nil, false, false, err
	}
	captured.latest = values
	captured.updated = result.Updated
	if !reference {
		captured.settled = MergeQueryRowMark(captured.settled, mark)
	}
	return values, true, result.Updated, nil
}

func (f *ForeignQuerySource) recheckRow(ctx context.Context, source *Source, visible int, row []sqltype.Value) (bool, error) {
	provider, ok := f.Wrapper.(catalog.ForeignScanRechecker)
	if !ok {
		return true, nil
	}
	cells := make(catalog.ForeignRow, len(row))
	_, output := source.LiveHost().Environments()
	for i, value := range row {
		cell, err := f.ExportValue(ctx, value, output)
		if err != nil {
			return false, err
		}
		if bytes, ok := cell.([]byte); ok {
			cell = slices.Clone(bytes)
		}
		cells[i] = cell
	}
	request := *f.captured[visible].request
	request.ForeignRequest = CopyForeignRequest(request.ForeignRequest)
	pass, err := provider.RecheckForeignScan(ctx, request, cells)
	return pass, ForeignCallbackError(err)
}
