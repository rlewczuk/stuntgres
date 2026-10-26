package fdw

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

// WriteRuntime is what the write protocol needs from the statement:
// participant enlistment, modification tracking for statement end, and the
// value environments. The engine's statementHost implements it.
type WriteRuntime interface {
	EnlistForeign(ctx context.Context, wrapper catalog.ForeignDataWrapper, request catalog.ForeignRequest, writing bool) (catalog.ForeignTransaction, error)
	TrackForeignModification(m *Modification)
	Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment)
}

type Modification struct {
	Source         *exec.ForeignQuerySource
	Request        catalog.ForeignModifyRequest
	State          any
	Copy           bool
	entered, ended bool
	endError       error
}

func OperationSupported(wrapper catalog.ForeignDataWrapper, op catalog.ForeignModifyOperation, table string) error {
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
func ModifyAdmission(view catalog.Read, relation *catalog.Relation, operation catalog.ForeignModifyOperation) error {
	server, ok := view.Foreign(relation.Foreign.Server)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	wrapper, ok := view.Foreign(server.Wrapper)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	handler, err := HandlerFor(view, wrapper)
	if err != nil {
		return err
	}
	if err := OperationSupported(handler.Wrapper, operation, relation.Name()); err != nil {
		return err
	}
	if operation != catalog.ForeignInsert {
		if _, ok := handler.Wrapper.(catalog.ForeignUpdateTargetProvider); !ok {
			return sqlError("0A000", "foreign modification requires row identity support")
		}
	}
	return nil
}

func (m *Modification) Begin(ctx context.Context, rt WriteRuntime) error {
	tx, err := rt.EnlistForeign(ctx, m.Source.Wrapper, m.Request.ForeignRequest, true)
	if err != nil {
		return err
	}
	m.Request.Transaction = tx
	m.entered = true
	rt.TrackForeignModification(m)
	if m.Copy {
		if beginner, ok := m.Source.Wrapper.(catalog.ForeignInsertBeginner); ok {
			m.State, err = beginner.BeginForeignInsert(ctx, catalog.ForeignInsertRequest{ForeignRequest: exec.CopyForeignRequest(m.Request.ForeignRequest), Copy: true})
		}
	} else if beginner, ok := m.Source.Wrapper.(catalog.ForeignModifyBeginner); ok {
		m.State, err = beginner.BeginForeignModify(ctx, m.CallbackRequest())
	}
	return exec.ForeignCallbackError(err)
}

func (m *Modification) CallbackRequest() catalog.ForeignModifyRequest {
	r := m.Request
	r.ForeignRequest = exec.CopyForeignRequest(r.ForeignRequest)
	r.Targets.Names = slices.Clone(r.Targets.Names)
	return r
}

func (m *Modification) End(ctx context.Context) error {
	if m.ended || !m.entered {
		return m.endError
	}
	m.ended = true
	if m.Copy {
		if ender, ok := m.Source.Wrapper.(catalog.ForeignInsertEnder); ok {
			m.endError = ender.EndForeignInsert(context.WithoutCancel(ctx), catalog.ForeignInsertRequest{ForeignRequest: exec.CopyForeignRequest(m.Request.ForeignRequest), Copy: true}, m.State)
		}
	} else if ender, ok := m.Source.Wrapper.(catalog.ForeignModifyEnder); ok {
		m.endError = ender.EndForeignModify(context.WithoutCancel(ctx), m.CallbackRequest(), m.State)
	}
	m.endError = exec.ForeignCallbackError(m.endError)
	m.State = nil
	return m.endError
}

func (m *Modification) Export(ctx context.Context, row []sqltype.Value, rt WriteRuntime) (catalog.ForeignRow, error) {
	_, output := rt.Environments()
	out := make(catalog.ForeignRow, len(row))
	for i, value := range row {
		cell, err := m.Source.ExportValue(ctx, value, output)
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

func (m *Modification) Returned(ctx context.Context, row catalog.ForeignRow, rt WriteRuntime) ([]sqltype.Value, error) {
	input, output := rt.Environments()
	return m.Source.ImportValues(ctx, row, nil, input, output, nil, nil)
}

func (m *Modification) InsertRows(ctx context.Context, rt WriteRuntime, rows [][]sqltype.Value, rowByRow bool, accept func([]sqltype.Value) error) (int64, error) {
	batch, hasBatch := m.Source.Wrapper.(catalog.ForeignBatchInserter)
	sizer, hasSize := m.Source.Wrapper.(catalog.ForeignBatchSizer)
	size := 1
	if hasBatch && hasSize && !rowByRow {
		var err error
		size, err = sizer.GetForeignModifyBatchSize(ctx, m.CallbackRequest(), m.State)
		if err != nil {
			return 0, exec.ForeignCallbackError(err)
		}
		if size < 1 {
			return 0, errors.New("stuntgres: foreign batch size must be positive")
		}
	}
	var count int64
	if size == 1 {
		inserter := m.Source.Wrapper.(catalog.ForeignInserter)
		for _, input := range rows {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			cells, err := m.Export(ctx, input, rt)
			if err != nil {
				return 0, err
			}
			result, present, err := inserter.ExecForeignInsert(ctx, m.CallbackRequest(), m.State, cells)
			if err != nil {
				return 0, exec.ForeignCallbackError(err)
			}
			if !present {
				continue
			}
			row, err := m.Returned(ctx, result, rt)
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
			inputs[i], err = m.Export(ctx, rows[offset+i], rt)
			if err != nil {
				return 0, err
			}
		}
		results, err := batch.ExecForeignBatchInsert(ctx, m.CallbackRequest(), m.State, inputs)
		if err != nil {
			return 0, exec.ForeignCallbackError(err)
		}
		if len(results) > n {
			return 0, errors.New("stuntgres: foreign batch returned more rows than supplied")
		}
		for _, result := range results {
			row, err := m.Returned(ctx, result, rt)
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

type TruncateGroup struct {
	Source  *exec.ForeignQuerySource
	Request catalog.ForeignTruncateRequest
}

// GroupTruncate groups the selected foreign tables by server, in first-seen
// order, so each server gets one truncate request with its tables in
// selection order.
func GroupTruncate(sources []*exec.ForeignQuerySource, cascade, restartIdentity bool) []TruncateGroup {
	var groups []TruncateGroup
	for _, f := range sources {
		index := -1
		for i, g := range groups {
			if g.Source.Request.ServerOID == f.Request.ServerOID {
				index = i
				break
			}
		}
		if index < 0 {
			index = len(groups)
			groups = append(groups, TruncateGroup{Source: f, Request: catalog.ForeignTruncateRequest{Cascade: cascade, RestartIdentity: restartIdentity}})
		}
		groups[index].Request.Tables = append(groups[index].Request.Tables, exec.CopyForeignRequest(f.Request))
	}
	return groups
}

// SetTransaction gives the group's request and each of its tables the
// server's participant transaction.
func (g *TruncateGroup) SetTransaction(tx catalog.ForeignTransaction) {
	g.Request.Transaction = tx
	for j := range g.Request.Tables {
		g.Request.Tables[j].Transaction = tx
	}
}

// PlanModification checks the operation and runs the wrapper's row identity
// and planning callbacks. The caller has released the replacement read.
func PlanModification(ctx context.Context, source *exec.ForeignQuerySource, operation catalog.ForeignModifyOperation, copying bool) (*Modification, error) {
	if err := OperationSupported(source.Wrapper, operation, source.Request.Table); err != nil {
		return nil, err
	}
	m := &Modification{Source: source, Request: catalog.ForeignModifyRequest{ForeignRequest: exec.CopyForeignRequest(source.Request), Operation: operation}, Copy: copying}
	if operation != catalog.ForeignInsert {
		provider, ok := source.Wrapper.(catalog.ForeignUpdateTargetProvider)
		if !ok {
			return nil, sqlError("0A000", "foreign modification requires row identity support")
		}
		targets, err := provider.AddForeignUpdateTargets(ctx, exec.CopyForeignRequest(source.Request), operation)
		if err != nil {
			return nil, exec.ForeignCallbackError(err)
		}
		if len(targets.Names) == 0 {
			return nil, errors.New("stuntgres: foreign row identity has no attributes")
		}
		targets.Names = slices.Clone(targets.Names)
		m.Request.Targets = targets
	}
	if !copying {
		if planner, ok := source.Wrapper.(catalog.ForeignModifyPlanner); ok {
			targets := m.Request.Targets
			targets.Names = slices.Clone(targets.Names)
			plan, err := planner.PlanForeignModify(ctx, exec.CopyForeignRequest(source.Request), operation, targets)
			if err != nil {
				return nil, exec.ForeignCallbackError(err)
			}
			m.Request.Plan = plan
		}
	}
	return m, nil
}
