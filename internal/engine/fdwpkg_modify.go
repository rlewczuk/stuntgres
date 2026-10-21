package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// foreignWriteRuntime is what the write protocol needs from the statement:
// participant enlistment, modification tracking for statement end, and the
// value environments. statementHost implements it.
type foreignWriteRuntime interface {
	EnlistForeign(ctx context.Context, wrapper catalog.ForeignDataWrapper, request catalog.ForeignRequest, writing bool) (catalog.ForeignTransaction, error)
	TrackForeignModification(m *foreignModification)
	Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment)
}

type foreignModification struct {
	source         *exec.ForeignQuerySource
	request        catalog.ForeignModifyRequest
	state          any
	copy           bool
	entered, ended bool
	endError       error
}

func foreignOperationSupported(wrapper catalog.ForeignDataWrapper, op catalog.ForeignModifyOperation, table string) error {
	var ok bool
	var verb string
	switch op {
	case catalog.ForeignInsert:
		_, ok = wrapper.(catalog.ForeignInserter)
		verb = "insert into"
	case catalog.ForeignUpdate:
		_, ok = wrapper.(catalog.ForeignUpdater)
		verb = "update"
	case catalog.ForeignDelete:
		_, ok = wrapper.(catalog.ForeignDeleter)
		verb = "delete from"
	}
	if !ok {
		return sqlError("0A000", fmt.Sprintf("cannot %s foreign table %q", verb, table))
	}
	return nil
}

// Metadata-only admission also runs for prepared CTE auxiliaries, before any
// auxiliary executor can open a source or enter a provider transaction.
func foreignModifyAdmission(view catalog.Read, relation *catalog.Relation, operation catalog.ForeignModifyOperation) error {
	server, ok := view.Foreign(relation.Foreign.Server)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	wrapper, ok := view.Foreign(server.Wrapper)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	handler, err := foreignHandlerFor(view, wrapper)
	if err != nil {
		return err
	}
	if err := foreignOperationSupported(handler.Wrapper, operation, relation.Name()); err != nil {
		return err
	}
	if operation != catalog.ForeignInsert {
		if _, ok := handler.Wrapper.(catalog.ForeignUpdateTargetProvider); !ok {
			return sqlError("0A000", "foreign modification requires row identity support")
		}
	}
	return nil
}

func (m *foreignModification) begin(ctx context.Context, rt foreignWriteRuntime) error {
	tx, err := rt.EnlistForeign(ctx, m.source.Wrapper, m.request.ForeignRequest, true)
	if err != nil {
		return err
	}
	m.request.Transaction = tx
	m.entered = true
	rt.TrackForeignModification(m)
	if m.copy {
		if beginner, ok := m.source.Wrapper.(catalog.ForeignInsertBeginner); ok {
			m.state, err = beginner.BeginForeignInsert(ctx, catalog.ForeignInsertRequest{ForeignRequest: exec.CopyForeignRequest(m.request.ForeignRequest), Copy: true})
		}
	} else if beginner, ok := m.source.Wrapper.(catalog.ForeignModifyBeginner); ok {
		m.state, err = beginner.BeginForeignModify(ctx, m.callbackRequest())
	}
	return exec.ForeignCallbackError(err)
}

func (m *foreignModification) callbackRequest() catalog.ForeignModifyRequest {
	r := m.request
	r.ForeignRequest = exec.CopyForeignRequest(r.ForeignRequest)
	r.Targets.Names = slices.Clone(r.Targets.Names)
	return r
}

func (m *foreignModification) end(ctx context.Context) error {
	if m.ended || !m.entered {
		return m.endError
	}
	m.ended = true
	if m.copy {
		if ender, ok := m.source.Wrapper.(catalog.ForeignInsertEnder); ok {
			m.endError = ender.EndForeignInsert(context.WithoutCancel(ctx), catalog.ForeignInsertRequest{ForeignRequest: exec.CopyForeignRequest(m.request.ForeignRequest), Copy: true}, m.state)
		}
	} else if ender, ok := m.source.Wrapper.(catalog.ForeignModifyEnder); ok {
		m.endError = ender.EndForeignModify(context.WithoutCancel(ctx), m.callbackRequest(), m.state)
	}
	m.endError = exec.ForeignCallbackError(m.endError)
	m.state = nil
	return m.endError
}

func (m *foreignModification) export(ctx context.Context, row []sqltype.Value, rt foreignWriteRuntime) (catalog.ForeignRow, error) {
	_, output := rt.Environments()
	out := make(catalog.ForeignRow, len(row))
	for i, value := range row {
		cell, err := m.source.ExportValue(ctx, value, output)
		if err != nil {
			return nil, err
		}
		if bytes, ok := cell.([]byte); ok {
			cell = slices.Clone(bytes)
		}
		out[i] = cell
	}
	return out, nil
}

func (m *foreignModification) returned(ctx context.Context, row catalog.ForeignRow, rt foreignWriteRuntime) ([]sqltype.Value, error) {
	input, output := rt.Environments()
	return m.source.ImportValues(ctx, row, nil, input, output, nil)
}

func (m *foreignModification) insertRows(ctx context.Context, rt foreignWriteRuntime, rows [][]sqltype.Value, rowByRow bool, accept func([]sqltype.Value) error) (int64, error) {
	batch, hasBatch := m.source.Wrapper.(catalog.ForeignBatchInserter)
	sizer, hasSize := m.source.Wrapper.(catalog.ForeignBatchSizer)
	size := 1
	if hasBatch && hasSize && !rowByRow {
		var err error
		size, err = sizer.GetForeignModifyBatchSize(ctx, m.callbackRequest(), m.state)
		if err != nil {
			return 0, exec.ForeignCallbackError(err)
		}
		if size < 1 {
			return 0, errors.New("stuntgres: foreign batch size must be positive")
		}
	}
	var count int64
	if size == 1 {
		inserter := m.source.Wrapper.(catalog.ForeignInserter)
		for _, input := range rows {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			cells, err := m.export(ctx, input, rt)
			if err != nil {
				return 0, err
			}
			result, present, err := inserter.ExecForeignInsert(ctx, m.callbackRequest(), m.state, cells)
			if err != nil {
				return 0, exec.ForeignCallbackError(err)
			}
			if !present {
				continue
			}
			row, err := m.returned(ctx, result, rt)
			if err != nil {
				return 0, err
			}
			if accept != nil {
				if err := accept(row); err != nil {
					return 0, err
				}
			}
			count++
		}
		return count, nil
	}
	for offset := 0; offset < len(rows); {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n := min(size, len(rows)-offset)
		inputs := make([]catalog.ForeignRow, n)
		for i := range inputs {
			var err error
			inputs[i], err = m.export(ctx, rows[offset+i], rt)
			if err != nil {
				return 0, err
			}
		}
		results, err := batch.ExecForeignBatchInsert(ctx, m.callbackRequest(), m.state, inputs)
		if err != nil {
			return 0, exec.ForeignCallbackError(err)
		}
		if len(results) > n {
			return 0, errors.New("stuntgres: foreign batch returned more rows than supplied")
		}
		for _, result := range results {
			row, err := m.returned(ctx, result, rt)
			if err != nil {
				return 0, err
			}
			if accept != nil {
				if err := accept(row); err != nil {
					return 0, err
				}
			}
			count++
		}
		offset += n
	}
	return count, nil
}

type foreignTruncateGroup struct {
	source  *exec.ForeignQuerySource
	request catalog.ForeignTruncateRequest
}
