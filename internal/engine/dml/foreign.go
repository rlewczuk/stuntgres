package dml

import (
	"context"
	"database/sql/driver"
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// ForeignCommand is one foreign-table INSERT, UPDATE or DELETE: the binding,
// the modification kind, the captured foreign target and query Inputs, the
// canonical parameters, the statement evaluation, the INSERT ... SELECT query
// root, the catalog of the command's authority and the bound VALUES rows.
type ForeignCommand struct {
	Binding     analyze.Binding
	Op          catalog.ForeignModifyOperation
	Foreign     *exec.ForeignQuerySource
	Inputs      *exec.Source
	Parameters  []sqltype.Value
	Evaluation  exec.Context
	QueryRoot   plan.Node
	Catalog     catalog.Read
	Expressions [][]expr.Expr
}

// ExecuteForeign runs a foreign-table INSERT, UPDATE or DELETE after the
// root has captured the foreign target and the query sources, released
// Engine.mu and the replacement read: it plans the modification, enters it,
// then inserts the evaluated rows or updates and deletes the rows the
// identity scan returns. The caller closes Inputs.
func ExecuteForeign(h Host, in ForeignCommand) (Outcome, error) {
	b, op, inputs, parameters := in.Binding, in.Op, in.Inputs, in.Parameters
	m, err := fdw.PlanModification(h.Context(), in.Foreign, op, false)
	if err != nil {
		return Outcome{}, err
	}
	input, output := h.Environments()
	state := &exec.State{Source: inputs, Parameters: parameters, Input: input, Output: output, Ctes: h.CTEInvocation()}
	defer state.ReleaseMemberships()
	m.Request.Parameters, err = exec.ForeignParameters(h.Context(), state, in.Foreign)
	if err != nil {
		return Outcome{}, err
	}
	if err = m.Begin(h.Context(), h); err != nil {
		return Outcome{}, err
	}
	if err = exec.SelectQuerySources(h.Context(), inputs); err != nil {
		return Outcome{}, err
	}
	evaluation := in.Evaluation
	evaluation.Query = state
	evaluation.Scope, evaluation.Frame = 1, 1
	evaluation.Host = h
	evaluation.PhysicalTable = b.Ref // Actual relation identity, never a fabricated row ID.
	if op == catalog.ForeignInsert {
		var rows [][]sqltype.Value
		if b.CommandQuery {
			b.Query = &plan.Query{Root: in.QueryRoot}
			input, output := h.Environments()
			batch, err := EvaluateInsertQuery(h.Context(), b, inputs, parameters, in.Catalog.Get(b.Relation.Namespace()).Name(), input, output, h)
			if err != nil {
				return Outcome{}, err
			}
			rows = batch.Values
		} else {
			rows = make([][]sqltype.Value, 0, len(b.Insert.Rows))
			for i, raw := range b.Insert.Rows {
				row := make([]sqltype.Value, len(b.Relation.Columns))
				for j, c := range b.Relation.Columns {
					row[j] = sqltype.Null(c.Usage.Type)
				}
				if len(b.Insert.Projection) != 0 {
					if err := EvaluateInsertValuesRow(in.Catalog, &b, in.Expressions[i], raw, row, len(b.Insert.Rows) > 1, &evaluation); err != nil {
						return Outcome{}, err
					}
				} else {
					for j, defaultExpr := range b.Insert.Defaults {
						if defaultExpr == nil {
							continue
						}
						supplied := false
						for k, target := range b.Targets {
							if target == j && b.Expressions[i][k] != nil && (len(b.ArrayTargets) == 0 || b.ArrayTargets[k] == nil) {
								supplied = true
								break
							}
						}
						if !supplied {
							row[j], err = defaultExpr.Eval(&evaluation)
							if err != nil {
								return Outcome{}, err
							}
						}
					}
					for j, rawValue := range raw {
						if b.Expressions[i][j] == nil {
							continue
						}
						target := b.Targets[j]
						if len(b.ArrayTargets) != 0 && b.ArrayTargets[j] != nil {
							row[target], err = b.ArrayTargets[j].ApplyExpression(&evaluation, row[target], in.Expressions[i][j])
						} else {
							row[target], err = in.Expressions[i][j].Eval(&evaluation)
						}
						if err != nil {
							return Outcome{}, analyze.InsertValueError(in.Catalog, err, b.Relation.Columns[target], rawValue, nil)
						}
					}
				}
				if err := table.EvaluateGenerations(h.Context(), b.Relation, row, &evaluation); err != nil {
					return Outcome{}, err
				}
				rows = append(rows, row)
			}
		}
		return foreignInsertResult(h, m, b, rows, &evaluation)
	}
	return executeForeignRowMutation(h, m, b, state, &evaluation)
}

func foreignInsertResult(h Host, m *fdw.Modification, b analyze.Binding, rows [][]sqltype.Value, evaluation *exec.Context) (Outcome, error) {
	var output *exec.FinalRows
	if b.Returning != nil {
		output = exec.NewFinalRows(b.Returning.Schema, 0)
		_, output.Output = h.Environments()
	}
	rowByRow := b.Returning != nil || b.ViewWrite != nil && len(view.ViewWriteOf(b.ViewWrite).Checks) != 0
	count, err := m.InsertRows(h.Context(), h, rows, rowByRow, func(row []sqltype.Value) error {
		if err := CheckViewRow(h, &b, row, evaluation); err != nil {
			return err
		}
		return ProjectForeignReturning(b, output, row, nil, evaluation)
	})
	if err != nil {
		if output != nil {
			output.Close()
		}
		return Outcome{}, err
	}
	return finishForeignResult(h, b, output, count)
}
func finishForeignResult(h Host, b analyze.Binding, output *exec.FinalRows, count int64) (Outcome, error) {
	if output != nil && b.Returning.RegclassOutput && count != 0 {
		if err := h.CaptureRegclassOutput(output); err != nil {
			output.Close()
			return Outcome{}, err
		}
	}
	if err := h.Context().Err(); err != nil {
		if output != nil {
			output.Close()
		}
		return Outcome{}, err
	}
	return Outcome{Count: count, Rows: output}, nil
}

func foreignTargetRows(ctx context.Context, h Host, m *fdw.Modification) ([]ForeignMutationRow, error) {
	f := m.Source
	r := exec.CopyForeignRequest(m.Request.ForeignRequest)
	r.Identity = &m.Request.Targets
	execution := &exec.ForeignExecution{}
	planning := exec.CopyForeignRequest(r)
	planning.Transaction = nil
	if err := f.Plan(ctx, planning, execution); err != nil {
		return nil, err
	}
	f.Executions = append(f.Executions, execution)
	scan, err := f.Wrapper.BeginForeignScan(ctx, exec.CopyForeignRequest(r), execution.Plan)
	execution.Scan = scan
	if err != nil {
		return nil, exec.ForeignCallbackError(err)
	}
	identityScan, ok := scan.(catalog.ForeignIdentityScan)
	if !ok {
		return nil, sqlError("0A000", "foreign modification scan does not supply row identity")
	}
	var rows []ForeignMutationRow
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, identity, present, err := identityScan.IterateForeignIdentity(ctx)
		if err != nil {
			return nil, exec.ForeignCallbackError(err)
		}
		if !present {
			break
		}
		if len(identity) != len(m.Request.Targets.Names) {
			return nil, errors.New("stuntgres: foreign row identity width does not match its descriptor")
		}
		for _, cell := range identity {
			if !driver.IsValue(cell) {
				return nil, errors.New("stuntgres: foreign row identity contains a non-driver value")
			}
			if !fdw.IdentityComparable(cell) {
				return nil, errors.New("stuntgres: foreign row identity contains a noncomparable driver value")
			}
		}
		input, output := h.Environments()
		values, err := f.ImportValues(ctx, row, execution.Slots, input, output, nil)
		if err != nil {
			return nil, err
		}
		rows = append(rows, ForeignMutationRow{Row: values, Identity: exec.CopyForeignCells(identity)})
	}
	return rows, nil
}
func executeForeignRowMutation(h Host, m *fdw.Modification, b analyze.Binding, state *exec.State, evaluation *exec.Context) (Outcome, error) {
	ctx := h.Context()
	rows, err := foreignTargetRows(ctx, h, m)
	if err != nil {
		return Outcome{}, err
	}
	var from plan.Node
	predicate := b.Predicate
	sourceWidth := b.Delete.SourceWidth
	if b.Update != nil {
		from, predicate, sourceWidth = b.Update.From, b.Update.Predicate, b.Update.SourceWidth
	} else {
		from = b.Delete.Using
	}
	var stage exec.Stage
	var frame []sqltype.Value
	sourceCount := 1
	if from != nil {
		state.VirtualReads = exec.QueryHasVirtualReads(from) || exec.BoundHasVirtualReads(predicate)
		if b.Update != nil {
			for _, expression := range b.Update.Assignments {
				state.VirtualReads = state.VirtualReads || exec.BoundHasVirtualReads(expression)
			}
			for _, target := range b.Update.ArrayTargets {
				state.VirtualReads = state.VirtualReads || exec.AssignmentTargetHasVirtualReads(target)
			}
		}
		if b.Returning != nil {
			for _, expression := range b.Returning.Expressions {
				state.VirtualReads = state.VirtualReads || exec.BoundHasVirtualReads(expression)
			}
		}
		stage, err = exec.OpenNode(ctx, state, from)
		if err != nil {
			return Outcome{}, err
		}
		defer stage.Close()
		if err := stage.Drain(ctx); err != nil {
			return Outcome{}, err
		}
		sourceCount = stage.Count()
		if sourceCount != 0 {
			frame = make([]sqltype.Value, len(b.Relation.Columns)+sourceWidth)
		}
	}
	var output *exec.FinalRows
	keep := false
	if b.Returning != nil {
		output = exec.NewFinalRows(b.Returning.Schema, 0)
		_, output.Output = h.Environments()
		defer func() {
			if !keep {
				output.Close()
			}
		}()
	}
	var seen fdw.IdentitySet
	var count int64
	for _, candidate := range rows {
		identityChecked := false
		for i := range sourceCount {
			if err := ctx.Err(); err != nil {
				return Outcome{}, err
			}
			evaluation.Row = candidate.Row
			if from != nil {
				copy(frame, candidate.Row)
				copy(frame[len(b.Relation.Columns):], stage.Source.Row(stage.Index(i)))
				evaluation.Row = frame
				evaluation.BindQueryIdentity(stage.Source, stage.Index(i))
			}
			matches, err := exec.UpdateMatches(evaluation, predicate)
			if err != nil {
				return Outcome{}, err
			}
			if !matches {
				continue
			}
			if identityChecked {
				continue
			}
			identityChecked = true
			if !seen.Add(candidate.Identity) {
				continue
			}
			var returned catalog.ForeignRow
			var present bool
			if b.Update != nil {
				row, err := EvaluateUpdateAssignments(evaluation, b.Update, b.Targets, candidate.Row)
				if err != nil {
					return Outcome{}, err
				}
				if err := table.EvaluateGenerations(ctx, b.Relation, row, evaluation); err != nil {
					return Outcome{}, err
				}
				input, err := m.Export(ctx, row, h)
				if err != nil {
					return Outcome{}, err
				}
				returned, present, err = m.Source.Wrapper.(catalog.ForeignUpdater).ExecForeignUpdate(ctx, m.CallbackRequest(), m.State, exec.CopyForeignCells(candidate.Identity), input)
				if err != nil {
					return Outcome{}, exec.ForeignCallbackError(err)
				}
			} else {
				input, exportErr := m.Export(ctx, candidate.Row, h)
				if exportErr != nil {
					return Outcome{}, exportErr
				}
				returned, present, err = m.Source.Wrapper.(catalog.ForeignDeleter).ExecForeignDelete(ctx, m.CallbackRequest(), m.State, exec.CopyForeignCells(candidate.Identity), input)
			}
			if err != nil {
				return Outcome{}, exec.ForeignCallbackError(err)
			}
			if !present {
				continue
			}
			actual, err := m.Returned(ctx, returned, h)
			if err != nil {
				return Outcome{}, err
			}
			if b.Update != nil {
				if err := CheckViewRow(h, &b, actual, evaluation); err != nil {
					return Outcome{}, err
				}
			}
			if err := ProjectForeignReturning(b, output, actual, candidate.Row, evaluation); err != nil {
				return Outcome{}, err
			}
			count++
		}
	}
	result, err := finishForeignResult(h, b, output, count)
	keep = err == nil
	return result, err
}
