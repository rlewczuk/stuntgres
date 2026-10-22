package dml

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// EvaluateInsertQuery owns the source pin and all destination rows. Evaluation
// runs unlocked; only the complete, constrained batch reaches the write stage.
// A nil h evaluates without statement authority.
func EvaluateInsertQuery(ctx context.Context, b analyze.Binding, source *exec.Source, parameters []sqltype.Value, schema string, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, h Host) (index.WriteBatch, error) {
	defer source.Close()
	project, ok := b.Query.Root.(*plan.ProjectNode)
	if !ok {
		return index.WriteBatch{}, fmt.Errorf("stuntgres: INSERT query lacks final projection")
	}
	// Bind uniqueness before opening the producer: opening can itself evaluate
	// rows. A target without unique indexes needs no per-row catalog recapture;
	// its epoch is still checked when the completed batch reaches Apply.
	unique := len(b.Relation.Constraints.Unique) != 0
	if h != nil && h.Command().PartitionInsert == nil && b.Relation.RelationKind != catalog.ForeignRelation {
		bound, err := BindUniqueWrite(h, b.Ref)
		if err != nil {
			return index.WriteBatch{}, err
		}
		unique = len(bound.Keys) != 0 || bound.Checked
	}
	state := &exec.State{Source: source, SourceMode: exec.SourceReusable, Parameters: parameters, Input: input, Output: output, Locking: source.Locks != nil}
	defer state.ReleaseMemberships()
	stage, err := exec.OpenQueryProjectStage(ctx, state, project)
	if err != nil {
		return index.WriteBatch{}, err
	}
	return AssembleInsertRows(ctx, b, &stage, -1, schema, input, output, h, unique, parameters, state)
}

// AssembleInsertRows produces, validates and admits the destination rows of
// cursor (count rows, or until EOF when count is negative). A nil h
// assembles without statement authority.
func AssembleInsertRows(ctx context.Context, b analyze.Binding, cursor exec.RowCursor, count int, schema string, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, h Host, unique bool, parameters []sqltype.Value, query *exec.State) (rows index.WriteBatch, err error) {
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
	var command *Command
	var op *session.Operation
	var runtime exec.Host
	if h != nil {
		command, op, runtime = h.Command(), h.Operation(), h
	}
	if command == nil || command.InsertReturning == nil {
		if count >= 0 {
			rows.Values = make([][]sqltype.Value, 0, count)
		} else {
			rows.Values = make([][]sqltype.Value, 0)
		}
	}
	scratch := make([]sqltype.Value, len(b.Targets))
	defaultSource, _ := cursor.(interface{ rowDefaults() []bool })
	var copyInsert *CopyInsertionState
	if producer, ok := cursor.(CopyInsertionCursor); ok && command != nil && (unique || command.PartitionInsert != nil) {
		copyInsert = NewCopyInsertionState(producer, b)
		unique = false // COPY admission belongs to its genuine flush, not decode.
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Parameters: parameters, Input: input, Output: output, Host: runtime}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Query: query}}
	copyInput := op != nil && op.CopyInput.Active.Load()
	for consumed := 0; count < 0 || consumed < count; consumed++ {
		if copyInput {
			op.CopyInput.Decoding.Store(true)
			ctx = op.Context()
		} else if err := ctx.Err(); err != nil {
			return index.WriteBatch{}, err
		}
		present, err := cursor.Next(ctx, scratch)
		if copyInput {
			op.CopyInput.Decoding.Store(false)
			ctx = op.Context()
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
				row[j], err = view.EvalBindingDefault(&b, &evaluation, j, expression)
				if err != nil {
					return index.WriteBatch{}, err
				}
			}
		}
		candidate := index.WriteCandidate{Values: row}
		if command != nil && command.PartitionInsert != nil {
			if err := ValidateInsertRow(h, b, schema, row, &evaluation); err != nil {
				return index.WriteBatch{}, err
			}
			if copyInsert != nil {
				recordCopyInsertion(copyInsert, h, b, &candidate, len(rows.Values))
				rows.Append(candidate)
				if copyInsert.Full() {
					if err := flushCopyInsertion(copyInsert, h, b, &rows, &evaluation); err != nil {
						return index.WriteBatch{}, err
					}
				}
				continue
			}
			if command.InsertReturning != nil {
				if _, err := AdmitInsertProducer(h, b, &candidate, index.WriteBatch{}, &evaluation); err != nil {
					return index.WriteBatch{}, err
				}
				continue
			}
			if err := checkInsertUniqueRow(h, b.Ref, &candidate, rows); err != nil {
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
		if command != nil {
			if err := command.RLS.CheckProposal(row, &evaluation); err != nil {
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
				if h == nil {
					return index.WriteBatch{}, fmt.Errorf("stuntgres: NOT NULL disclosure requires statement authority")
				}
				return index.WriteBatch{}, RowConstraintError(h, b, schema, c.Name, "", row)
			}
		}
		if len(b.Relation.Constraints.Check) != 0 {
			constraint, checkErr := table.ValidateCheckRow(b.Relation, row, &evaluation)
			if checkErr != nil {
				return index.WriteBatch{}, checkErr
			}
			if constraint != nil {
				if h == nil {
					return index.WriteBatch{}, fmt.Errorf("stuntgres: CHECK disclosure requires statement authority")
				}
				return index.WriteBatch{}, RowConstraintError(h, b, schema, "", constraint.Name, row)
			}
		}
		if unique || command != nil && command.InsertReturning != nil {
			if h == nil {
				return index.WriteBatch{}, fmt.Errorf("stuntgres: uniqueness requires statement authority")
			}
			admitted, err := AdmitInsertProducer(h, b, &candidate, rows, &evaluation)
			if err != nil {
				return index.WriteBatch{}, err
			}
			if !admitted {
				continue
			}
		}
		if b.ViewWrite != nil {
			if err := CheckViewRow(h, &b, row, &evaluation); err != nil {
				return index.WriteBatch{}, err
			}
		}
		if copyInsert != nil {
			recordCopyInsertion(copyInsert, h, b, &candidate, len(rows.Values))
		}
		rows.Append(candidate)
		if copyInsert != nil && copyInsert.Full() {
			if err := flushCopyInsertion(copyInsert, h, b, &rows, &evaluation); err != nil {
				return index.WriteBatch{}, err
			}
		}
	}
	if copyInsert != nil && len(copyInsert.Lines) != 0 {
		if err := flushCopyInsertion(copyInsert, h, b, &rows, &evaluation); err != nil {
			return index.WriteBatch{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return index.WriteBatch{}, err
	}
	return rows, nil
}
