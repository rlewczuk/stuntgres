package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Ordinary single VALUES is a destination-attribute projection. Multirow VALUES
// first demands the complete source row, like the shared query source cursor.
// Bound expressions and imported parameters survive USER pruning in both cases.
func EvaluateInsertValuesRow(read catalog.Read, b *analyze.Binding, supplied []expr.Expr, syntax []parser.Expr, row []sqltype.Value, sourceFirst bool, evaluation *exec.Context) error {
	if sourceFirst {
		for i, expression := range supplied {
			if expression == nil {
				continue
			}
			target := b.Targets[i]
			value, err := expression.Eval(evaluation)
			if err != nil {
				return analyze.InsertValueError(read, err, b.Relation.Columns[target], syntax[i], nil)
			}
			if len(b.Insert.IdentityDiscard) == 0 || !b.Insert.IdentityDiscard[target] {
				if len(b.ArrayTargets) != 0 && b.ArrayTargets[i] != nil {
					value, err = b.ArrayTargets[i].Apply(evaluation, row[target], value)
					if err != nil {
						return err
					}
				}
				row[target] = value
			}
		}
	}
	for target, source := range b.Insert.Projection {
		discard := len(b.Insert.IdentityDiscard) != 0 && b.Insert.IdentityDiscard[target]
		if source >= 0 && supplied[source] != nil && !discard {
			if !sourceFirst {
				if len(b.ArrayTargets) != 0 && b.ArrayTargets[source] != nil {
					for i, slot := range b.Targets {
						if slot != target || supplied[i] == nil {
							continue
						}
						value, err := b.ArrayTargets[i].ApplyExpression(evaluation, row[target], supplied[i])
						if err != nil {
							return err
						}
						row[target] = value
					}
					continue
				}
				value, err := supplied[source].Eval(evaluation)
				if err != nil {
					return analyze.InsertValueError(read, err, b.Relation.Columns[target], syntax[source], nil)
				}
				row[target] = value
			}
			continue
		}
		if len(b.Insert.Defaults) != 0 && b.Insert.Defaults[target] != nil {
			value, err := view.EvalBindingDefault(b, evaluation, target, b.Insert.Defaults[target])
			if err != nil {
				return err
			}
			row[target] = value
		}
	}
	return nil
}

// evaluateInsertLiteral is the existing VALUES/default producer, before row
// triggers, conflict checks and admission. Observation wraps this work once.
func evaluateInsertLiteral(in *InsertInput, i int, row []sqltype.Value) error {
	b, evaluation := &in.Binding, in.Evaluation
	if len(b.Insert.Projection) != 0 {
		return EvaluateInsertValuesRow(in.Catalog, b, in.Expressions[i], b.Insert.Rows[i], row, len(b.Insert.Rows) > 1, evaluation)
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
				value, err := view.EvalBindingDefault(b, evaluation, j, expression)
				if err != nil {
					return err
				}
				row[j] = value
			}
		}
	}
	for j, raw := range b.Insert.Rows[i] {
		if b.Expressions[i][j] == nil {
			continue
		}
		target := b.Targets[j]
		var value sqltype.Value
		var err error
		if len(b.ArrayTargets) != 0 && b.ArrayTargets[j] != nil {
			value, err = b.ArrayTargets[j].ApplyExpression(evaluation, row[target], in.Expressions[i][j])
		} else {
			value, err = in.Expressions[i][j].Eval(evaluation)
		}
		if err != nil {
			return analyze.InsertValueError(in.Catalog, err, b.Relation.Columns[target], raw, nil)
		}
		row[target] = value
	}
	return nil
}
