package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// evaluateInsertQuery owns the source pin and all destination rows. Evaluation
// runs unlocked; only the complete, constrained batch reaches WriteStage.Apply.
func evaluateInsertQuery(ctx context.Context, b analyze.Binding, source *exec.Source, parameters []sqltype.Value, schema string, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, scope *StatementScope) (index.WriteBatch, error) {
	defer source.Close()
	project, ok := b.Query.Root.(*plan.ProjectNode)
	if !ok {
		return index.WriteBatch{}, fmt.Errorf("stuntgres: INSERT query lacks final projection")
	}
	// Bind uniqueness before opening the producer: opening can itself evaluate
	// rows. A target without unique indexes needs no per-row catalog recapture;
	// its epoch is still checked when the completed batch reaches Apply.
	unique := len(b.Relation.Constraints.Unique) != 0
	if scope != nil && scope.partitionInsert == nil && b.Relation.RelationKind != catalog.ForeignRelation {
		bound, err := scope.bindUniqueWrite(b.Ref)
		if err != nil {
			return index.WriteBatch{}, err
		}
		unique = len(bound.Keys) != 0 || bound.Checked
	}
	state := &exec.State{Source: source, Parameters: parameters, Input: input, Output: output, Locking: source.Locks != nil}
	defer state.ReleaseMemberships()
	stage, err := exec.OpenQueryProjectStage(ctx, state, project)
	if err != nil {
		return index.WriteBatch{}, err
	}
	return assembleInsertRows(ctx, b, &stage, -1, schema, input, output, scope, unique, parameters, state)
}

func assembleInsertRows(ctx context.Context, b analyze.Binding, cursor exec.RowCursor, count int, schema string, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, scope *StatementScope, unique bool, parameters []sqltype.Value, query *exec.State) (rows index.WriteBatch, err error) {
	// A required Close error vetoes the batch, but never hides a primary failure.
	defer func() {
		closeErr := cursor.Close()
		if err == nil {
			if closeErr != nil {
				err = closeErr
			} else {
				err = ctx.Err()
			}
			if err != nil {
				rows = index.WriteBatch{}
			}
		}
	}()
	if scope == nil || scope.insertReturning == nil {
		if count >= 0 {
			rows.Values = make([][]sqltype.Value, 0, count)
		} else {
			rows.Values = make([][]sqltype.Value, 0)
		}
	}
	scratch := make([]sqltype.Value, len(b.Targets))
	defaultSource, _ := cursor.(interface{ rowDefaults() []bool })
	var copyInsert *copyInsertionState
	if producer, ok := cursor.(copyInsertionCursor); ok && scope != nil && (unique || scope.partitionInsert != nil) {
		copyInsert = newCopyInsertionState(producer, b)
		unique = false // COPY admission belongs to its genuine flush, not decode.
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Parameters: parameters, Input: input, Output: output, Host: hostOf(scope)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Query: query}}
	copyInput := scope != nil && scope.op.CopyInput.Active.Load()
	for consumed := 0; count < 0 || consumed < count; consumed++ {
		if copyInput {
			scope.op.CopyInput.Decoding.Store(true)
			ctx = scope.op.Context()
		} else if err := ctx.Err(); err != nil {
			return index.WriteBatch{}, err
		}
		present, err := cursor.Next(ctx, scratch)
		if copyInput {
			scope.op.CopyInput.Decoding.Store(false)
			ctx = scope.op.Context()
			evaluation.Context = ctx
		}
		if err != nil {
			return index.WriteBatch{}, err
		}
		if !present {
			if count >= 0 {
				return index.WriteBatch{}, fmt.Errorf("stuntgres: early INSERT source EOF")
			}
			break
		}
		row := make([]sqltype.Value, len(b.Relation.Columns))
		for j, c := range b.Relation.Columns {
			row[j] = sqltype.Null(c.Usage.Type)
		}
		for j, target := range b.Targets {
			value := scratch[j]
			if len(b.ArrayTargets) != 0 && b.ArrayTargets[j] != nil {
				value, err = b.ArrayTargets[j].Apply(&evaluation, row[target], value)
				if err != nil {
					return index.WriteBatch{}, err
				}
			}
			row[target] = value
		}
		for j, expression := range b.Insert.Defaults {
			if expression != nil && (defaultSource == nil || defaultSource.rowDefaults()[j]) {
				row[j], err = evalBindingDefault(&b, &evaluation, j, expression)
				if err != nil {
					return index.WriteBatch{}, err
				}
			}
		}
		candidate := index.WriteCandidate{Values: row}
		if scope != nil && scope.partitionInsert != nil {
			if err := scope.engine().validateInsertRow(scope, b, schema, row, &evaluation); err != nil {
				return index.WriteBatch{}, err
			}
			if copyInsert != nil {
				copyInsert.record(scope, b, &candidate, len(rows.Values))
				rows.Append(candidate)
				if copyInsert.full() {
					if err := copyInsert.flush(scope, b, &rows, &evaluation); err != nil {
						return index.WriteBatch{}, err
					}
				}
				continue
			}
			if scope.insertReturning != nil {
				if _, err := scope.admitInsertProducer(b, &candidate, index.WriteBatch{}, &evaluation); err != nil {
					return index.WriteBatch{}, err
				}
				continue
			}
			if err := scope.checkInsertUniqueRow(b.Ref, &candidate, rows); err != nil {
				return index.WriteBatch{}, err
			}
			rows.Append(candidate)
			continue
		}
		if err := table.EvaluateGenerations(ctx, b.Relation, row, &evaluation); err != nil {
			return index.WriteBatch{}, err
		}
		if b.Relation.RelationKind == catalog.ForeignRelation {
			rows.Append(candidate)
			continue
		}
		if scope != nil {
			if err := access.RowSecurityProposal(scope.rls.proposal, scope.rls.proposalTable); err != nil {
				return index.WriteBatch{}, err
			}
		}
		for j, c := range b.Relation.Columns {
			if err := ctx.Err(); err != nil {
				return index.WriteBatch{}, err
			}
			if c.Nullable {
				continue
			}
			value, err := table.LogicalColumnValue(b.Relation, j, row, &evaluation)
			if err != nil {
				return index.WriteBatch{}, err
			}
			if value.IsNull() {
				if scope == nil {
					return index.WriteBatch{}, fmt.Errorf("stuntgres: NOT NULL disclosure requires statement authority")
				}
				return index.WriteBatch{}, scope.engine().rowConstraintError(scope, b, schema, c.Name, "", row)
			}
		}
		if len(b.Relation.Constraints.Check) != 0 {
			constraint, checkErr := table.ValidateCheckRow(b.Relation, row, &evaluation)
			if checkErr != nil {
				return index.WriteBatch{}, checkErr
			}
			if constraint != nil {
				if scope == nil {
					return index.WriteBatch{}, fmt.Errorf("stuntgres: CHECK disclosure requires statement authority")
				}
				return index.WriteBatch{}, scope.engine().rowConstraintError(scope, b, schema, "", constraint.Name, row)
			}
		}
		if unique || scope != nil && scope.insertReturning != nil {
			if scope == nil {
				return index.WriteBatch{}, fmt.Errorf("stuntgres: uniqueness requires statement authority")
			}
			admitted, err := scope.admitInsertProducer(b, &candidate, rows, &evaluation)
			if err != nil {
				return index.WriteBatch{}, err
			}
			if !admitted {
				continue
			}
		}
		if b.ViewWrite != nil {
			if err := scope.engine().checkViewRow(scope, &b, row, &evaluation); err != nil {
				return index.WriteBatch{}, err
			}
		}
		if copyInsert != nil {
			copyInsert.record(scope, b, &candidate, len(rows.Values))
		}
		rows.Append(candidate)
		if copyInsert != nil && copyInsert.full() {
			if err := copyInsert.flush(scope, b, &rows, &evaluation); err != nil {
				return index.WriteBatch{}, err
			}
		}
	}
	if copyInsert != nil && len(copyInsert.lines) != 0 {
		if err := copyInsert.flush(scope, b, &rows, &evaluation); err != nil {
			return index.WriteBatch{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return index.WriteBatch{}, err
	}
	return rows, nil
}
