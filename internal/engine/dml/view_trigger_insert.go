package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

func viewTriggerInsert(h Host, in ViewTriggerCommand, state *exec.State, evaluation *exec.Context, accept func([]sqltype.Value) error) (err error) {
	b := in.Binding
	newRow := func() []sqltype.Value {
		row := make([]sqltype.Value, len(b.Relation.Columns))
		for i, c := range b.Relation.Columns {
			row[i] = sqltype.Null(c.Usage.Type)
		}
		return row
	}
	if b.CommandQuery {
		project, ok := in.QueryRoot.(*plan.ProjectNode)
		if !ok {
			return sqlError("XX000", "INSERT query lacks final projection")
		}
		borrowed := *state
		borrowed.SourceMode = exec.SourceBorrowed
		defer borrowed.ReleaseMemberships()
		cursor, failure := exec.OpenQueryProjectStage(h.Context(), &borrowed, project)
		if failure != nil {
			return failure
		}
		defer func() {
			if failure := cursor.Close(); err == nil {
				err = failure
			}
		}()
		scratch := make([]sqltype.Value, len(b.Targets))
		for {
			present, failure := cursor.Next(h.Context(), scratch)
			if failure != nil {
				return failure
			}
			if !present {
				return nil
			}
			row := newRow()
			for j, target := range b.Targets {
				value := scratch[j]
				if len(b.ArrayTargets) != 0 && b.ArrayTargets[j] != nil {
					value, failure = b.ArrayTargets[j].Apply(evaluation, row[target], value)
					if failure != nil {
						return failure
					}
				}
				row[target] = value
			}
			for j, expression := range b.Insert.Defaults {
				if expression != nil {
					row[j], failure = view.EvalBindingDefault(&b, evaluation, j, expression)
					if failure != nil {
						return failure
					}
				}
			}
			if failure = accept(row); failure != nil {
				return failure
			}
		}
	}
	for i, raw := range b.Insert.Rows {
		if err := h.Context().Err(); err != nil {
			return err
		}
		row := newRow()
		if len(b.Insert.Projection) != 0 {
			if err := EvaluateInsertValuesRow(in.Catalog, &b, in.Expressions[i], raw, row, len(b.Insert.Rows) > 1, evaluation); err != nil {
				return err
			}
		} else {
			for j, expression := range b.Insert.Defaults {
				if expression == nil {
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
					row[j], err = view.EvalBindingDefault(&b, evaluation, j, expression)
					if err != nil {
						return err
					}
				}
			}
			for j, rawValue := range raw {
				if b.Expressions[i][j] == nil {
					continue
				}
				target := b.Targets[j]
				if len(b.ArrayTargets) != 0 && b.ArrayTargets[j] != nil {
					row[target], err = b.ArrayTargets[j].ApplyExpression(evaluation, row[target], in.Expressions[i][j])
				} else {
					row[target], err = in.Expressions[i][j].Eval(evaluation)
				}
				if err != nil {
					return analyze.InsertValueError(in.Catalog, err, b.Relation.Columns[target], rawValue, nil)
				}
			}
		}
		if err = accept(row); err != nil {
			return err
		}
	}
	return nil
}
