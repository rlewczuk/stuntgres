package engine

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// InsertInput is an INSERT captured under Engine.mu: its binding and
// catalog, and either the selected query source of INSERT ... SELECT or the
// VALUES evaluation.
type InsertInput struct {
	// Context is the operation context the caller captured at entry.
	Context context.Context
	Binding analyze.Binding
	Catalog catalog.Read
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
func ExecuteInsert(h dml.Host, in InsertInput) (dml.Outcome, error) {
	ctx, b := in.Context, in.Binding
	if in.Query != nil {
		input, output := h.Environments()
		rows, err := EvaluateInsertQuery(ctx, b, in.Query, in.Parameters, in.Schema, input, output, h)
		if err != nil {
			return dml.Outcome{}, err
		}
		if err := ctx.Err(); err != nil {
			return dml.Outcome{}, err
		}
		return finishInsert(h, b.Ref, rows)
	}
	command := h.Command()
	evaluation := in.Evaluation
	var err error
	var rows index.WriteBatch
	if command.InsertReturning == nil {
		rows.Values = make([][]sqltype.Value, 0, len(b.Insert.Rows))
	}
	for i, source := range b.Insert.Rows {
		if err = ctx.Err(); err != nil {
			return dml.Outcome{}, err
		}
		row := make([]sqltype.Value, len(b.Relation.Columns))
		for j, c := range b.Relation.Columns {
			row[j] = sqltype.Null(c.Usage.Type)
		}
		if len(b.Insert.Projection) != 0 {
			if err = EvaluateInsertValuesRow(in.Catalog, &b, in.Expressions[i], source, row, len(b.Insert.Rows) > 1, evaluation); err != nil {
				return dml.Outcome{}, err
			}
			schema := in.Catalog.Get(b.Relation.Namespace()).Name()
			if err = ValidateInsertRow(h, b, schema, row, evaluation); err != nil {
				return dml.Outcome{}, err
			}
			candidate := index.WriteCandidate{Values: row}
			admitted, admitErr := AdmitInsertProducer(h, b, &candidate, rows, evaluation)
			if admitErr != nil {
				return dml.Outcome{}, admitErr
			}
			if admitted {
				if err := CheckViewRow(h, &b, row, evaluation); err != nil {
					return dml.Outcome{}, err
				}
				rows.Append(candidate)
			}
			continue
		}
		if len(b.Insert.Defaults) != 0 {
			supplied := b.Expressions[i]
			for j, target := range b.Targets {
				if j < len(supplied) && supplied[j] != nil && (len(b.ArrayTargets) == 0 || b.ArrayTargets[j] == nil) {
					row[target] = sqltype.Value{}
				}
			}
			for j, expression := range b.Insert.Defaults {
				if expression != nil && row[j].Type() != nil {
					row[j], err = view.EvalBindingDefault(&b, evaluation, j, expression)
					if err != nil {
						return dml.Outcome{}, err
					}
				}
			}
		}
		for j, v := range source {
			if b.Expressions[i][j] == nil {
				continue
			}
			target := b.Targets[j]
			c := b.Relation.Columns[target]
			var value sqltype.Value
			if len(b.ArrayTargets) != 0 && b.ArrayTargets[j] != nil {
				value, err = b.ArrayTargets[j].ApplyExpression(evaluation, row[target], in.Expressions[i][j])
			} else {
				value, err = in.Expressions[i][j].Eval(evaluation)
			}
			if err == nil {
				row[target] = value
			}
			if err != nil {
				return dml.Outcome{}, analyze.InsertValueError(in.Catalog, err, c, v, nil)
			}
		}
		schema := in.Catalog.Get(b.Relation.Namespace()).Name()
		if err = ValidateInsertRow(h, b, schema, row, evaluation); err != nil {
			return dml.Outcome{}, err
		}
		candidate := index.WriteCandidate{Values: row}
		admitted, admitErr := AdmitInsertProducer(h, b, &candidate, rows, evaluation)
		if admitErr != nil {
			return dml.Outcome{}, admitErr
		}
		if admitted {
			if err := CheckViewRow(h, &b, row, evaluation); err != nil {
				return dml.Outcome{}, err
			}
			rows.Append(candidate)
		}
	}
	if err := ctx.Err(); err != nil {
		return dml.Outcome{}, err
	}
	return finishInsert(h, b.Ref, rows)
}

// finishInsert finishes RETURNING, or stages the validated batch.
func finishInsert(h dml.Host, ref ids.ObjectRef, rows index.WriteBatch) (dml.Outcome, error) {
	if h.Command().InsertReturning != nil {
		return finishInsertReturning(h)
	}
	count, err := ApplyInsertValidated(h, ref, rows)
	if err != nil {
		return dml.Outcome{}, err
	}
	return dml.Outcome{Count: int64(count)}, nil
}
