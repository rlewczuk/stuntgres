package dml

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// InsertInput is an INSERT captured under Engine.mu: its binding and
// catalog, and either the selected query source of INSERT ... SELECT or the
// VALUES evaluation.
type InsertInput struct {
	// Context is the operation context the caller captured at entry.
	Context           context.Context
	Binding           analyze.Binding
	Catalog           catalog.Read
	DefaultOccurrence exec.DefaultOccurrence // Original preparation ownership, not runtime row identity.
	// Query is INSERT ... SELECT's selected (and, for view writes, frozen)
	// source; nil selects VALUES. Parameters are the canonical parameters
	// and Schema the target's schema name.
	Query      *exec.Source
	Parameters []sqltype.Value
	Schema     string
	// Evaluation is the VALUES statement evaluation and Expressions the bound
	// row expressions.
	Evaluation  *exec.Context
	Expressions [][]expr.Expr
}

// ExecuteInsert runs a captured INSERT VALUES or INSERT ... SELECT with the
// caller holding nothing: it evaluates, validates and admits every row, then
// stages the batch or finishes RETURNING.
func ExecuteInsert(h Host, in InsertInput) (outcome Outcome, failure error) {
	ctx, b := in.Context, in.Binding
	if err := BeforeStatement(h, b, stored.TriggerInsert); err != nil {
		if in.Query != nil {
			in.Query.Close()
		}
		return Outcome{}, err
	}
	// Completion is deferred before the alternate event so conflict UPDATE
	// qualifies before INSERT, matching their AFTER statement registration.
	defer CompleteAfterStatement(h, b, stored.TriggerInsert, &outcome, &failure)
	if b.Insert.Conflict != nil && b.Insert.Conflict.Update != nil {
		update := b
		update.Targets = b.Insert.Conflict.UpdateTargets
		if err := BeforeStatement(h, update, stored.TriggerUpdate); err != nil {
			if in.Query != nil {
				in.Query.Close()
			}
			return Outcome{}, err
		}
		if err := RegisterAfterStatement(h, update, stored.TriggerUpdate); err != nil {
			if in.Query != nil {
				in.Query.Close()
			}
			return Outcome{}, err
		}
		defer CompleteAfterStatement(h, update, stored.TriggerUpdate, &outcome, &failure)
	}
	if err := RegisterAfterStatement(h, b, stored.TriggerInsert); err != nil {
		if in.Query != nil {
			in.Query.Close()
		}
		return Outcome{}, err
	}
	if in.Query != nil {
		input, output := h.Environments()
		rows, err := EvaluateInsertQuery(ctx, b, in.Query, in.Parameters, in.Schema, input, output, h, in.DefaultOccurrence)
		if err != nil {
			return Outcome{}, err
		}
		if err := ctx.Err(); err != nil {
			return Outcome{}, err
		}
		return finishInsert(ctx, h, b.Ref, rows)
	}
	command := h.Command()
	evaluation := in.Evaluation
	var err error
	var rows index.WriteBatch
	if command.InsertReturning == nil {
		rows.Values = make([][]sqltype.Value, 0, len(b.Insert.Rows))
	}
	input := exec.DMLObservationOf(ctx).Role(exec.DMLInsertInput)
	input.BeginLoop()
	for i := range b.Insert.Rows {
		if err = ctx.Err(); err != nil {
			return Outcome{}, err
		}
		input.StartWork()
		row := make([]sqltype.Value, len(b.Relation.Columns))
		for j, c := range b.Relation.Columns {
			row[j] = sqltype.Null(c.Usage.Type)
		}
		err = evaluateInsertLiteral(&in, i, row)
		input.EndWork()
		if err != nil {
			return Outcome{}, err
		}
		input.Emit()
		schema := in.Catalog.Get(b.Relation.Namespace()).Name()
		proceed, failure := ValidateInsertRow(h, b, schema, row, evaluation)
		if failure != nil {
			return Outcome{}, failure
		}
		if !proceed {
			continue
		}
		candidate := index.WriteCandidate{Values: row}
		admitted, admitErr := AdmitInsertProducer(ctx, h, b, &candidate, rows, evaluation)
		if admitErr != nil {
			return Outcome{}, admitErr
		}
		if admitted {
			if err := CheckViewRow(h, &b, row, evaluation); err != nil {
				return Outcome{}, err
			}
			rows.Append(candidate)
		}
	}
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	return finishInsert(ctx, h, b.Ref, rows)
}

// finishInsert finishes RETURNING, or stages the validated batch.
func finishInsert(ctx context.Context, h Host, ref ids.ObjectRef, rows index.WriteBatch) (Outcome, error) {
	if h.Command().InsertReturning != nil {
		return finishInsertReturning(h)
	}
	count, err := ApplyInsertValidated(ctx, h, ref, rows)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Count: int64(count)}, nil
}
