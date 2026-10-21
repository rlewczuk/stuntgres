package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// executeInsertLocked runs INSERT VALUES and INSERT ... SELECT. It is entered
// with Engine.mu held and releases it before evaluating rows.
func (e *Engine) executeInsertLocked(scope *StatementScope, execution *preparedCommandExecution, canonical []sqltype.Value) (Result, error) {
	ctx := scope.op.Context()
	var err error
	b, auth, evaluation := execution.binding, execution.auth, execution.evaluation
	queryRoot, expressions := execution.queryRoot, execution.expressions
	if err := e.captureCheckCommentSnapshotLocked(scope, b.Relation); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err := scope.capturePartitionInsertLocked(e, auth, b.Relation); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	scope.beginInsertReturning(b)
	keepReturning := false
	if scope.insertReturning != nil {
		defer func() {
			if !keepReturning {
				scope.insertReturning.rows.Close()
			}
		}()
	}
	if b.CommandQuery {
		source, sourceErr := e.captureQuerySourcesLocked(scope, b.QuerySources, len(b.QuerySources) == 0)
		if sourceErr != nil {
			e.mu.Unlock()
			return Result{}, sourceErr
		}
		schema := auth.Catalog.Get(b.Relation.Namespace()).Name()
		e.mu.Unlock()
		if err := exec.SelectQuerySources(ctx, source); err != nil {
			return Result{}, err
		}
		if b.ViewWrite != nil || b.Rule.Template {
			source = freezeViewWriteSources(scope, source, b.QuerySources)
		}
		b.Query = &plan.Query{Root: queryRoot}
		rows, queryErr := evaluateInsertQuery(ctx, b, source, canonical, schema, scope.input, scope.output, scope)
		if queryErr != nil {
			return Result{}, queryErr
		}
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if scope.insertReturning != nil {
			result, err := scope.finishInsertReturning()
			keepReturning = err == nil
			return result, err
		}
		count, err := scope.applyInsertValidated(b.Ref, rows)
		if err != nil {
			return Result{}, err
		}
		return Result{Count: int64(count)}, nil
	}
	if b.SubqueryDemand {
		inputs, sourceErr := e.captureQuerySourcesLocked(scope, b.QuerySources, false)
		if sourceErr != nil {
			e.mu.Unlock()
			return Result{}, sourceErr
		}
		state := &exec.State{Source: inputs, Parameters: canonical, Input: scope.input, Output: scope.output, Locking: inputs.Locks != nil, Ctes: scope.cteInvocation}
		defer state.ReleaseMemberships()
		evaluation.Query = state
		evaluation.Scope, evaluation.Frame = 1, 1
	}
	e.mu.Unlock()
	if evaluation.Query != nil {
		if err := exec.SelectQuerySources(ctx, evaluation.Query.Source); err != nil {
			return Result{}, err
		}
		if b.ViewWrite != nil || b.Rule.Template {
			evaluation.Query.Source = freezeViewWriteSources(scope, evaluation.Query.Source, b.QuerySources)
		}
		defer evaluation.Query.Source.Close()
	}
	var rows index.WriteBatch
	if scope.insertReturning == nil {
		rows.Values = make([][]sqltype.Value, 0, len(b.Insert.Rows))
	}
	for i, source := range b.Insert.Rows {
		if err = ctx.Err(); err != nil {
			return Result{}, err
		}
		row := make([]sqltype.Value, len(b.Relation.Columns))
		for j, c := range b.Relation.Columns {
			row[j] = sqltype.Null(c.Usage.Type)
		}
		if len(b.Insert.Projection) != 0 {
			if err = evaluateInsertValuesRow(auth.Catalog, &b, expressions[i], source, row, len(b.Insert.Rows) > 1, &evaluation); err != nil {
				return Result{}, err
			}
			schema := auth.Catalog.Get(b.Relation.Namespace()).Name()
			if err = e.validateInsertRow(scope, b, schema, row, &evaluation); err != nil {
				return Result{}, err
			}
			candidate := index.WriteCandidate{Values: row}
			admitted, admitErr := scope.admitInsertProducer(b, &candidate, rows, &evaluation)
			if admitErr != nil {
				return Result{}, admitErr
			}
			if admitted {
				if err := e.checkViewRow(scope, &b, row, &evaluation); err != nil {
					return Result{}, err
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
					row[j], err = evalBindingDefault(&b, &evaluation, j, expression)
					if err != nil {
						return Result{}, err
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
				value, err = b.ArrayTargets[j].ApplyExpression(&evaluation, row[target], expressions[i][j])
			} else {
				value, err = expressions[i][j].Eval(&evaluation)
			}
			if err == nil {
				row[target] = value
			}
			if err != nil {
				return Result{}, analyze.InsertValueError(auth.Catalog, err, c, v, nil)
			}
		}
		schema := auth.Catalog.Get(b.Relation.Namespace()).Name()
		if err = e.validateInsertRow(scope, b, schema, row, &evaluation); err != nil {
			return Result{}, err
		}
		candidate := index.WriteCandidate{Values: row}
		admitted, admitErr := scope.admitInsertProducer(b, &candidate, rows, &evaluation)
		if admitErr != nil {
			return Result{}, admitErr
		}
		if admitted {
			if err := e.checkViewRow(scope, &b, row, &evaluation); err != nil {
				return Result{}, err
			}
			rows.Append(candidate)
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if scope.insertReturning != nil {
		result, err := scope.finishInsertReturning()
		keepReturning = err == nil
		return result, err
	}
	count, err := scope.applyInsertValidated(b.Ref, rows)
	if err != nil {
		return Result{}, err
	}
	return Result{Count: int64(count)}, nil
}
