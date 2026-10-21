package engine

import (
	"context"
	"database/sql/driver"
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Called at the common physical-target dispatch, after binding/ACL/read-only
// admission but before any physical data view or row-version acquisition.
func (e *Engine) executeForeignCommandLocked(s *StatementScope, stmt parser.Statement, execution *preparedCommandExecution, parameters []sqltype.Value) (Result, error) {
	b := execution.binding
	if b.Insert.Conflict != nil {
		e.mu.Unlock()
		return Result{}, sqlError("0A000", "ON CONFLICT on foreign tables is unsupported in embedded mode")
	}
	if b.CurrentOf != "" {
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
	if b.ViewWrite != nil {
		for _, layer := range viewWriteOf(b.ViewWrite).layers {
			invoker = layer.relation.Owner
			if viewOf(layer.relation).Invoker() {
				invoker = execution.auth.State.CurrentUser
			}
		}
	}
	target, err := e.captureForeignSourceLocked(s, b.Ref, b.Relation, invoker, nil, plan.RowMark{})
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err = fdw.OperationSupported(target.Foreign.Wrapper, op, b.Relation.Name()); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	inputs, err := e.captureQuerySourcesLocked(s, b.QuerySources, len(b.QuerySources) == 0)
	if err == nil && b.Returning != nil {
		err = e.captureCommentSnapshotLocked(s)
	}
	e.mu.Unlock()
	if inputs != nil {
		defer inputs.Close()
	}
	if err != nil {
		return Result{}, err
	}
	s.foreignUnlocked()
	m, err := s.planForeignModification(s.op.Context(), target.Foreign, op, false)
	if err != nil {
		return Result{}, err
	}
	state := &exec.State{Source: inputs, Parameters: parameters, Input: s.input, Output: s.output, Ctes: s.cteInvocation}
	defer state.ReleaseMemberships()
	m.Request.Parameters, err = exec.ForeignParameters(s.op.Context(), state, target.Foreign)
	if err != nil {
		return Result{}, err
	}
	if err = m.Begin(s.op.Context(), s.foreignWrites()); err != nil {
		return Result{}, err
	}
	if err = exec.SelectQuerySources(s.op.Context(), inputs); err != nil {
		return Result{}, err
	}
	evaluation := execution.evaluation
	evaluation.Query = state
	evaluation.Scope, evaluation.Frame = 1, 1
	evaluation.Host = hostOf(s)
	evaluation.PhysicalTable = b.Ref // Actual relation identity, never a fabricated row ID.
	if op == catalog.ForeignInsert {
		var rows [][]sqltype.Value
		if b.CommandQuery {
			b.Query = &plan.Query{Root: execution.queryRoot}
			batch, err := evaluateInsertQuery(s.op.Context(), b, inputs, parameters, execution.auth.Catalog.Get(b.Relation.Namespace()).Name(), s.input, s.output, s)
			if err != nil {
				return Result{}, err
			}
			rows = batch.values
		} else {
			rows = make([][]sqltype.Value, 0, len(b.Insert.Rows))
			for i, raw := range b.Insert.Rows {
				row := make([]sqltype.Value, len(b.Relation.Columns))
				for j, c := range b.Relation.Columns {
					row[j] = sqltype.Null(c.Usage.Type)
				}
				if len(b.Insert.Projection) != 0 {
					if err := evaluateInsertValuesRow(execution.auth.Catalog, &b, execution.expressions[i], raw, row, len(b.Insert.Rows) > 1, &evaluation); err != nil {
						return Result{}, err
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
								return Result{}, err
							}
						}
					}
					for j, rawValue := range raw {
						if b.Expressions[i][j] == nil {
							continue
						}
						target := b.Targets[j]
						if len(b.ArrayTargets) != 0 && b.ArrayTargets[j] != nil {
							row[target], err = b.ArrayTargets[j].ApplyExpression(&evaluation, row[target], execution.expressions[i][j])
						} else {
							row[target], err = execution.expressions[i][j].Eval(&evaluation)
						}
						if err != nil {
							return Result{}, analyze.InsertValueError(execution.auth.Catalog, err, b.Relation.Columns[target], rawValue, nil)
						}
					}
				}
				if err := evaluateGenerations(s.op.Context(), b.Relation, row, &evaluation); err != nil {
					return Result{}, err
				}
				rows = append(rows, row)
			}
		}
		return s.foreignInsertResult(m, b, rows, &evaluation)
	}
	return s.executeForeignRowMutation(m, b, state, &evaluation)
}

func (s *StatementScope) foreignInsertResult(m *fdw.Modification, b analyze.Binding, rows [][]sqltype.Value, evaluation *exec.Context) (Result, error) {
	var output *exec.FinalRows
	if b.Returning != nil {
		output = exec.NewFinalRows(b.Returning.Schema, 0)
		output.Output = s.output
	}
	rowByRow := b.Returning != nil || b.ViewWrite != nil && len(viewWriteOf(b.ViewWrite).checks) != 0
	count, err := m.InsertRows(s.op.Context(), s.foreignWrites(), rows, rowByRow, func(row []sqltype.Value) error {
		if err := s.engine().checkViewRow(s, &b, row, evaluation); err != nil {
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
func projectForeignReturning(b analyze.Binding, output *exec.FinalRows, row, old []sqltype.Value, evaluation *exec.Context) error {
	if output == nil {
		return nil
	}
	eval := *evaluation
	eval.Scope, eval.Frame = 1, 1
	eval.Row, eval.OldRow = row, old
	if b.Delete.Using != nil || b.Update != nil && b.Update.From != nil {
		// Keep the matched source suffix and query identity, replacing only
		// the target prefix with the provider's actual NEW/returned tuple.
		copy(evaluation.Row, row)
		eval.Row = evaluation.Row
	}
	for i, expression := range b.Returning.Expressions {
		value, err := expression.Eval(&eval)
		if err != nil {
			return err
		}
		if value.Type() == nil || value.Type() != output.Columns[i].Usage.Type {
			return errors.New("stuntgres: invalid foreign RETURNING value")
		}
		output.Values = append(output.Values, value)
	}
	output.Count++
	return nil
}
func (s *StatementScope) finishForeignResult(b analyze.Binding, output *exec.FinalRows, count int64) (Result, error) {
	if output != nil && b.Returning.RegclassOutput && count != 0 {
		if err := s.engine().captureRegclassOutput(s.op, output); err != nil {
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

func foreignTargetRows(ctx context.Context, s *StatementScope, m *fdw.Modification) ([]foreignMutationRow, error) {
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
	var rows []foreignMutationRow
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
		values, err := f.ImportValues(ctx, row, execution.Slots, s.input, s.output, nil)
		if err != nil {
			return nil, err
		}
		rows = append(rows, foreignMutationRow{row: values, identity: exec.CopyForeignCells(identity)})
	}
	return rows, nil
}
func (s *StatementScope) executeForeignRowMutation(m *fdw.Modification, b analyze.Binding, state *exec.State, evaluation *exec.Context) (Result, error) {
	ctx := s.op.Context()
	rows, err := foreignTargetRows(ctx, s, m)
	if err != nil {
		return Result{}, err
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
			return Result{}, err
		}
		defer stage.Close()
		if err := stage.Drain(ctx); err != nil {
			return Result{}, err
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
		output.Output = s.output
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
				return Result{}, err
			}
			evaluation.Row = candidate.row
			if from != nil {
				copy(frame, candidate.row)
				copy(frame[len(b.Relation.Columns):], stage.Source.Row(stage.Index(i)))
				evaluation.Row = frame
				evaluation.BindQueryIdentity(stage.Source, stage.Index(i))
			}
			matches, err := exec.UpdateMatches(evaluation, predicate)
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
			if !seen.Add(candidate.identity) {
				continue
			}
			var returned catalog.ForeignRow
			var present bool
			if b.Update != nil {
				row, err := evaluateUpdateAssignments(evaluation, b.Update, b.Targets, candidate.row)
				if err != nil {
					return Result{}, err
				}
				if err := evaluateGenerations(ctx, b.Relation, row, evaluation); err != nil {
					return Result{}, err
				}
				input, err := m.Export(ctx, row, s.foreignWrites())
				if err != nil {
					return Result{}, err
				}
				returned, present, err = m.Source.Wrapper.(catalog.ForeignUpdater).ExecForeignUpdate(ctx, m.CallbackRequest(), m.State, exec.CopyForeignCells(candidate.identity), input)
				if err != nil {
					return Result{}, exec.ForeignCallbackError(err)
				}
			} else {
				input, exportErr := m.Export(ctx, candidate.row, s.foreignWrites())
				if exportErr != nil {
					return Result{}, exportErr
				}
				returned, present, err = m.Source.Wrapper.(catalog.ForeignDeleter).ExecForeignDelete(ctx, m.CallbackRequest(), m.State, exec.CopyForeignCells(candidate.identity), input)
			}
			if err != nil {
				return Result{}, exec.ForeignCallbackError(err)
			}
			if !present {
				continue
			}
			actual, err := m.Returned(ctx, returned, s.foreignWrites())
			if err != nil {
				return Result{}, err
			}
			if b.Update != nil {
				if err := s.engine().checkViewRow(s, &b, actual, evaluation); err != nil {
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

// foreignTableScope is the statement's foreign-table slot: the foreign scan
// owner and cursor, the FDW participants and modifications of this statement,
// and the hold-cursor promotions owned by this completion.
type foreignTableScope struct {
	owner          *exec.ForeignScanOwner
	cursor         *sqlCursor
	statements     []*fdw.Participant
	modifications  []*fdw.Modification
	holdPromotions []*sqlCursor // Exact new promotions owned by this completion.
}

// foreignTables returns the statement's foreignTables slot, allocating it on first use.
func (s *StatementScope) foreignTables() *foreignTableScope {
	if s.foreignTablesSlot == nil {
		s.foreignTablesSlot = new(foreignTableScope)
	}
	return s.foreignTablesSlot
}

// foreignTablesOrZero reads the foreignTables slot; a nil slot reads as zero.
func (s *StatementScope) foreignTablesOrZero() foreignTableScope {
	if s.foreignTablesSlot == nil {
		return foreignTableScope{}
	}
	return *s.foreignTablesSlot
}

// takeOwner detaches the statement's foreign scan owner, if any.
func (f *foreignTableScope) takeOwner() *exec.ForeignScanOwner {
	if f == nil {
		return nil
	}
	owner := f.owner
	f.owner = nil
	return owner
}

// clearStatements forgets the statement's foreign participants.
func (f *foreignTableScope) clearStatements() {
	if f != nil {
		f.statements = nil
	}
}

// clearHoldPromotions forgets this completion's hold-cursor promotions.
func (f *foreignTableScope) clearHoldPromotions() {
	if f != nil {
		f.holdPromotions = nil
	}
}

// The setters below install a value, allocating the slot only for a
// non-nil value: a missing slot already reads as nil.

func (s *StatementScope) setForeignStatements(statements []*fdw.Participant) {
	if statements != nil || s.foreignTablesSlot != nil {
		s.foreignTables().statements = statements
	}
}

func (s *StatementScope) setForeignHoldPromotions(promotions []*sqlCursor) {
	if promotions != nil || s.foreignTablesSlot != nil {
		s.foreignTables().holdPromotions = promotions
	}
}

func (s *StatementScope) setForeignCursor(cursor *sqlCursor) {
	if cursor != nil || s.foreignTablesSlot != nil {
		s.foreignTables().cursor = cursor
	}
}

// setForeignLexical installs the scan owner and cursor saved around a SQL
// routine body.
func (s *StatementScope) setForeignLexical(owner *exec.ForeignScanOwner, cursor *sqlCursor) {
	if owner != nil || cursor != nil || s.foreignTablesSlot != nil {
		f := s.foreignTables()
		f.owner, f.cursor = owner, cursor
	}
}
