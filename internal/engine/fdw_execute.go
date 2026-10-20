package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Called at the common physical-target dispatch, after binding/ACL/read-only
// admission but before any physical data view or row-version acquisition.
func (e *Engine) executeForeignCommandLocked(s *StatementScope, stmt parser.Statement, execution *preparedCommandExecution, parameters []sqltype.Value) (Result, error) {
	b := execution.binding
	if b.insert.conflict != nil {
		e.mu.Unlock()
		return Result{}, sqlError("0A000", "ON CONFLICT on foreign tables is unsupported in embedded mode")
	}
	if b.currentOf != "" {
		e.mu.Unlock()
		return Result{}, sqlError("0A000", "positioned foreign modification is unsupported in embedded mode")
	}
	op := catalog.ForeignInsert
	switch stmt.(type) {
	case *parser.Update:
		op = catalog.ForeignUpdate
	case *parser.Delete:
		op = catalog.ForeignDelete
	}
	invoker := execution.auth.State.CurrentUser
	if b.viewWrite != nil {
		for _, layer := range b.viewWrite.layers {
			invoker = layer.relation.Owner
			if viewOf(layer.relation).invoker {
				invoker = execution.auth.State.CurrentUser
			}
		}
	}
	target, err := e.captureForeignSourceLocked(s, b.ref, b.relation, invoker, nil, queryRowMark{})
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err = foreignOperationSupported(target.foreign.wrapper, op, b.relation.Name()); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	inputs, err := e.captureQuerySourcesLocked(s, b.querySources, len(b.querySources) == 0)
	if err == nil && b.returning != nil {
		err = e.captureCommentSnapshotLocked(s)
	}
	e.mu.Unlock()
	if inputs != nil {
		defer inputs.close()
	}
	if err != nil {
		return Result{}, err
	}
	s.foreignUnlocked()
	m, err := s.planForeignModification(s.op.Context(), target.foreign, op, false)
	if err != nil {
		return Result{}, err
	}
	state := &queryState{source: inputs, parameters: parameters, input: s.input, output: s.output, ctes: s.cteInvocation}
	defer state.releaseMemberships()
	m.request.Parameters, err = foreignParameters(s.op.Context(), state, target.foreign)
	if err != nil {
		return Result{}, err
	}
	if err = m.begin(s.op.Context(), s); err != nil {
		return Result{}, err
	}
	if err = selectQuerySources(s.op.Context(), inputs); err != nil {
		return Result{}, err
	}
	evaluation := execution.evaluation
	evaluation.query = state
	evaluation.Scope, evaluation.Frame = 1, 1
	evaluation.statementScope = s
	evaluation.physicalTable = b.ref // Actual relation identity, never a fabricated row ID.
	if op == catalog.ForeignInsert {
		var rows [][]sqltype.Value
		if b.commandQuery {
			b.query = &BoundQuery{Root: execution.queryRoot}
			batch, err := evaluateInsertQuery(s.op.Context(), b, inputs, parameters, execution.auth.Catalog.Get(b.relation.Namespace()).Name(), s.input, s.output, s)
			if err != nil {
				return Result{}, err
			}
			rows = batch.values
		} else {
			rows = make([][]sqltype.Value, 0, len(b.insert.rows))
			for i, raw := range b.insert.rows {
				row := make([]sqltype.Value, len(b.relation.Columns))
				for j, c := range b.relation.Columns {
					row[j] = sqltype.Null(c.Usage.Type)
				}
				if len(b.insert.projection) != 0 {
					if err := evaluateInsertValuesRow(execution.auth.Catalog, &b, execution.expressions[i], raw, row, len(b.insert.rows) > 1, &evaluation); err != nil {
						return Result{}, err
					}
				} else {
					for j, defaultExpr := range b.insert.defaults {
						if defaultExpr == nil {
							continue
						}
						supplied := false
						for k, target := range b.targets {
							if target == j && b.expressions[i][k] != nil && (len(b.arrayTargets) == 0 || b.arrayTargets[k] == nil) {
								supplied = true
								break
							}
						}
						if !supplied {
							row[j], err = defaultExpr.Eval(&evaluation)
							if err != nil {
								return Result{}, err
							}
						}
					}
					for j, rawValue := range raw {
						if b.expressions[i][j] == nil {
							continue
						}
						target := b.targets[j]
						if len(b.arrayTargets) != 0 && b.arrayTargets[j] != nil {
							row[target], err = b.arrayTargets[j].applyExpression(&evaluation, row[target], execution.expressions[i][j])
						} else {
							row[target], err = execution.expressions[i][j].Eval(&evaluation)
						}
						if err != nil {
							return Result{}, insertValueError(execution.auth.Catalog, err, b.relation.Columns[target], rawValue, nil)
						}
					}
				}
				if err := evaluateGenerations(s.op.Context(), b.relation, row, &evaluation); err != nil {
					return Result{}, err
				}
				rows = append(rows, row)
			}
		}
		return s.foreignInsertResult(m, b, rows, &evaluation)
	}
	return s.executeForeignRowMutation(m, b, state, &evaluation)
}

func (s *StatementScope) foreignInsertResult(m *foreignModification, b binding, rows [][]sqltype.Value, evaluation *EvalContext) (Result, error) {
	var output *finalRows
	if b.returning != nil {
		output = newFinalRows(b.returning.schema, 0)
		output.output = s.output
	}
	rowByRow := b.returning != nil || b.viewWrite != nil && len(b.viewWrite.checks) != 0
	count, err := m.insertRows(s.op.Context(), s, rows, rowByRow, func(row []sqltype.Value) error {
		if err := s.op.session.Engine().checkViewRow(s, &b, row, evaluation); err != nil {
			return err
		}
		return projectForeignReturning(b, output, row, nil, evaluation)
	})
	if err != nil {
		if output != nil {
			output.Close()
		}
		return Result{}, err
	}
	return s.finishForeignResult(b, output, count)
}
func projectForeignReturning(b binding, output *finalRows, row, old []sqltype.Value, evaluation *EvalContext) error {
	if output == nil {
		return nil
	}
	eval := *evaluation
	eval.Scope, eval.Frame = 1, 1
	eval.Row, eval.OldRow = row, old
	if b.delete.using != nil || b.update != nil && b.update.from != nil {
		// Keep the matched source suffix and query identity, replacing only
		// the target prefix with the provider's actual NEW/returned tuple.
		copy(evaluation.Row, row)
		eval.Row = evaluation.Row
	}
	for i, expression := range b.returning.expressions {
		value, err := expression.Eval(&eval)
		if err != nil {
			return err
		}
		if value.Type() == nil || value.Type() != output.schema[i].Usage.Type {
			return errors.New("stuntgres: invalid foreign RETURNING value")
		}
		output.values = append(output.values, value)
	}
	output.count++
	return nil
}
func (s *StatementScope) finishForeignResult(b binding, output *finalRows, count int64) (Result, error) {
	if output != nil && b.returning.regclassOutput && count != 0 {
		if err := s.op.session.Engine().captureRegclassOutput(s.op, output); err != nil {
			output.Close()
			return Result{}, err
		}
	}
	if err := s.op.Context().Err(); err != nil {
		if output != nil {
			output.Close()
		}
		return Result{}, err
	}
	if output == nil {
		return Result{Count: count}, nil
	}
	return Result{Count: count, Rows: output}, nil
}

type foreignMutationRow struct {
	row      []sqltype.Value
	identity catalog.ForeignRowIdentity
}

func (m *foreignModification) targetRows(ctx context.Context, s *StatementScope) ([]foreignMutationRow, error) {
	f := m.source
	r := copyForeignRequest(m.request.ForeignRequest)
	r.Identity = &m.request.Targets
	execution := &foreignExecution{}
	planning := copyForeignRequest(r)
	planning.Transaction = nil
	if err := f.plan(ctx, planning, execution); err != nil {
		return nil, err
	}
	f.executions = append(f.executions, execution)
	scan, err := f.wrapper.BeginForeignScan(ctx, copyForeignRequest(r), execution.plan)
	execution.scan = scan
	if err != nil {
		return nil, foreignCallbackError(err)
	}
	identityScan, ok := scan.(catalog.ForeignIdentityScan)
	if !ok {
		return nil, sqlError("0A000", "foreign modification scan does not supply row identity")
	}
	var rows []foreignMutationRow
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, identity, present, err := identityScan.IterateForeignIdentity(ctx)
		if err != nil {
			return nil, foreignCallbackError(err)
		}
		if !present {
			break
		}
		if len(identity) != len(m.request.Targets.Names) {
			return nil, errors.New("stuntgres: foreign row identity width does not match its descriptor")
		}
		for _, cell := range identity {
			if !driver.IsValue(cell) {
				return nil, errors.New("stuntgres: foreign row identity contains a non-driver value")
			}
			if !foreignIdentityComparable(cell) {
				return nil, errors.New("stuntgres: foreign row identity contains a noncomparable driver value")
			}
		}
		values, err := f.importValues(ctx, row, execution.slots, s.input, s.output, nil)
		if err != nil {
			return nil, err
		}
		rows = append(rows, foreignMutationRow{row: values, identity: copyForeignCells(identity)})
	}
	return rows, nil
}
func foreignIdentityEqual(a, b catalog.ForeignRowIdentity) bool {
	if len(a) != len(b) {
		return false
	}
	for i, cell := range a {
		if x, ok := cell.([]byte); ok {
			y, ok := b[i].([]byte)
			if !ok || !bytes.Equal(x, y) {
				return false
			}
		} else if cell != b[i] {
			return false
		}
	}
	return true
}
func (s *StatementScope) executeForeignRowMutation(m *foreignModification, b binding, state *queryState, evaluation *EvalContext) (Result, error) {
	ctx := s.op.Context()
	rows, err := m.targetRows(ctx, s)
	if err != nil {
		return Result{}, err
	}
	var from LogicalNode
	predicate := b.predicate
	sourceWidth := b.delete.sourceWidth
	if b.update != nil {
		from, predicate, sourceWidth = b.update.from, b.update.predicate, b.update.sourceWidth
	} else {
		from = b.delete.using
	}
	var stage indexedStage
	var frame []sqltype.Value
	sourceCount := 1
	if from != nil {
		state.virtualReads = queryHasVirtualReads(from) || boundHasVirtualReads(predicate)
		if b.update != nil {
			for _, expression := range b.update.assignments {
				state.virtualReads = state.virtualReads || boundHasVirtualReads(expression)
			}
			for _, target := range b.update.arrayTargets {
				state.virtualReads = state.virtualReads || assignmentTargetHasVirtualReads(target)
			}
		}
		if b.returning != nil {
			for _, expression := range b.returning.expressions {
				state.virtualReads = state.virtualReads || boundHasVirtualReads(expression)
			}
		}
		stage, err = openQueryNode(ctx, state, from)
		if err != nil {
			return Result{}, err
		}
		defer stage.Close()
		if err := stage.drain(ctx); err != nil {
			return Result{}, err
		}
		sourceCount = stage.count()
		if sourceCount != 0 {
			frame = make([]sqltype.Value, len(b.relation.Columns)+sourceWidth)
		}
	}
	var output *finalRows
	keep := false
	if b.returning != nil {
		output = newFinalRows(b.returning.schema, 0)
		output.output = s.output
		defer func() {
			if !keep {
				output.Close()
			}
		}()
	}
	var seen foreignIdentitySet
	var count int64
	for _, candidate := range rows {
		identityChecked := false
		for i := range sourceCount {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			evaluation.Row = candidate.row
			if from != nil {
				copy(frame, candidate.row)
				copy(frame[len(b.relation.Columns):], stage.source.row(stage.index(i)))
				evaluation.Row = frame
				evaluation.bindQueryIdentity(stage.source, stage.index(i))
			}
			matches, err := updateMatches(evaluation, predicate)
			if err != nil {
				return Result{}, err
			}
			if !matches {
				continue
			}
			if identityChecked {
				continue
			}
			identityChecked = true
			if !seen.add(candidate.identity) {
				continue
			}
			var returned catalog.ForeignRow
			var present bool
			if b.update != nil {
				row, err := evaluateUpdateAssignments(evaluation, b.update, b.targets, candidate.row)
				if err != nil {
					return Result{}, err
				}
				if err := evaluateGenerations(ctx, b.relation, row, evaluation); err != nil {
					return Result{}, err
				}
				input, err := m.export(ctx, row, s)
				if err != nil {
					return Result{}, err
				}
				returned, present, err = m.source.wrapper.(catalog.ForeignUpdater).ExecForeignUpdate(ctx, m.callbackRequest(), m.state, copyForeignCells(candidate.identity), input)
				if err != nil {
					return Result{}, foreignCallbackError(err)
				}
			} else {
				input, exportErr := m.export(ctx, candidate.row, s)
				if exportErr != nil {
					return Result{}, exportErr
				}
				returned, present, err = m.source.wrapper.(catalog.ForeignDeleter).ExecForeignDelete(ctx, m.callbackRequest(), m.state, copyForeignCells(candidate.identity), input)
			}
			if err != nil {
				return Result{}, foreignCallbackError(err)
			}
			if !present {
				continue
			}
			actual, err := m.returned(ctx, returned, s)
			if err != nil {
				return Result{}, err
			}
			if b.update != nil {
				if err := s.op.session.Engine().checkViewRow(s, &b, actual, evaluation); err != nil {
					return Result{}, err
				}
			}
			if err := projectForeignReturning(b, output, actual, candidate.row, evaluation); err != nil {
				return Result{}, err
			}
			count++
		}
	}
	result, err := s.finishForeignResult(b, output, count)
	keep = err == nil
	return result, err
}
