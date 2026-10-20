package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Each Execute has a newly analyzed binding. Preflight mutates only that private
// binding, never a stored source/default definition or held prepared statement.
func preflightMerge(c *exec.Context, merge *analyze.MergeBinding, catalog catalog.Read) error {
	owner := *c
	owner.Scope, owner.Frame = 1, 1
	var err error
	merge.Source, err = preflightQuery(&owner, merge.Source)
	if err != nil {
		return err
	}
	if err := validateQueryJoinExecution(&owner, merge.Source, catalog, 0); err != nil {
		return err
	}
	merge.On, _, err = preflightCoercions(&owner, merge.On)
	if err != nil {
		return err
	}
	if err := validateExpressionJoinExecution(&owner, merge.On, catalog); err != nil {
		return err
	}
	for i := range merge.Arms {
		arm := &merge.Arms[i]
		arm.Condition, _, err = preflightCoercions(&owner, arm.Condition)
		if err != nil {
			return err
		}
		if err := validateExpressionJoinExecution(&owner, arm.Condition, catalog); err != nil {
			return err
		}
		if arm.Action == parser.MergeUpdateAction {
			arm.Input.Update, err = preflightUpdate(&owner, arm.Input.Update)
			if err != nil {
				return err
			}
			if err := validateMutationJoinExecution(&owner, &arm.Input, nil, catalog); err != nil {
				return err
			}
			continue
		}
		if arm.Action == parser.MergeDeleteAction || arm.Action == parser.MergeNothingAction {
			continue
		}
		for j, expression := range arm.Input.Expressions[0] {
			if len(arm.Input.Insert.IdentityDiscard) != 0 && arm.Input.Insert.IdentityDiscard[arm.Input.Targets[j]] {
				continue
			}
			folded, _, err := preflightCoercions(&owner, expression)
			if err != nil {
				return analyze.InsertValueError(catalog, err, arm.Input.Relation.Columns[arm.Input.Targets[j]], arm.Syntax[j], nil)
			}
			arm.Input.Expressions[0][j] = folded
		}
		for j, expression := range arm.Input.Insert.Defaults {
			if expression == nil {
				continue
			}
			defaultOwner := owner
			applyDefaultOrigin(bindingDefaultOrigin(&arm.Input, j), &defaultOwner)
			arm.Input.Insert.Defaults[j], _, err = preflightCoercionsWithPolicy(&defaultOwner, expression, true)
			if err != nil {
				return err
			}
		}
		if err := validateMutationJoinExecution(&owner, &arm.Input, arm.Input.Expressions, catalog); err != nil {
			return err
		}
	}
	if merge.HasBySource && merge.HasByTarget {
		var facts fullJoinFacts
		var err error
		properties := merge.On.Properties()
		_, literal := merge.On.(plan.LiteralExpr)
		if merge.On.ResultUsage().Type.BaseType().ID() != sqltype.Bool && (literal || properties.Volatility == expr.Immutable && !properties.RowDependent && !properties.StageDependent && !properties.RuntimeDomainConversion) {
			// MERGE admits integer/TEXT carriers independently of Boolean JOIN
			// predicates. Preserve that truth contract in constant capability facts
			// without replacing the expression or its analyzed dependencies.
			var value sqltype.Value
			value, err = merge.On.Eval(&owner)
			if err == nil {
				facts.truth, err = mergeOnTruth(value)
				facts.constant = true
			}
		} else {
			facts, err = fullJoinCondition(&owner, &plan.JoinNode{LeftWidth: merge.TargetWidth, RightWidth: merge.SourceWidth}, merge.On, false)
		}
		if err != nil {
			return err
		}
		if !facts.constant && len(facts.keys) == 0 {
			return sqlError("0A000", "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions")
		}
	}
	return nil
}

func mergeOnTruth(value sqltype.Value) (plan.Truth, error) {
	if value.Type().IsDomain() {
		var err error
		value, err = value.BaseValue()
		if err != nil {
			return plan.False, err
		}
	}
	if value.IsNull() {
		return plan.Unknown, nil
	}
	switch value.Type().ID() {
	case sqltype.Bool:
		return plan.TruthOf(value)
	case sqltype.Int2, sqltype.Int4, sqltype.Int8:
		i, err := value.Integer()
		if err != nil {
			return plan.False, err
		}
		if i != 0 {
			return plan.True, nil
		}
		return plan.False, nil
	case sqltype.Text:
		return plan.True, nil
	}
	return plan.False, fmt.Errorf("stuntgres: invalid MERGE ON carrier")
}

func mergeHasVirtualReads(merge *analyze.MergeBinding) bool {
	if exec.QueryHasVirtualReads(merge.Source) || exec.BoundHasVirtualReads(merge.On) {
		return true
	}
	for _, arm := range merge.Arms {
		if exec.BoundHasVirtualReads(arm.Condition) {
			return true
		}
		if arm.Input.Update != nil {
			for _, expr := range arm.Input.Update.Assignments {
				if exec.BoundHasVirtualReads(expr) {
					return true
				}
			}
			for _, target := range arm.Input.Update.ArrayTargets {
				if assignmentTargetHasVirtualReads(target) {
					return true
				}
			}
		}
		for _, row := range arm.Input.Expressions {
			for _, expr := range row {
				if exec.BoundHasVirtualReads(expr) {
					return true
				}
			}
		}
	}
	return false
}

func (e *Engine) executeMerge(ctx context.Context, scope *StatementScope, b analyze.Binding, target, inputs *exec.Source, parameters []sqltype.Value, schema string) (Result, error) {
	defer target.Close()
	defer inputs.Close()
	unique, err := scope.bindUniqueWrite(b.Ref)
	if err != nil {
		return Result{}, err
	}
	if err := exec.SelectQuerySources(ctx, inputs); err != nil {
		return Result{}, err
	}
	state := &exec.State{Source: inputs, Parameters: parameters, Input: scope.input, Output: scope.output, Locking: inputs.Locks != nil, VirtualReads: mergeHasVirtualReads(b.Merge)}
	if b.Returning != nil {
		for _, expr := range b.Returning.Expressions {
			state.VirtualReads = state.VirtualReads || exec.BoundHasVirtualReads(expr)
		}
	}
	defer state.ReleaseMemberships()
	cursor, err := exec.OpenNode(ctx, state, b.Merge.Source)
	if err != nil {
		return Result{}, err
	}
	rows, err := e.assembleMergeRows(ctx, scope, b, target, &cursor, state, schema, len(unique.keys) != 0 || unique.checked)
	if err != nil {
		return Result{}, err
	}
	complete := false
	defer func() {
		if !complete && rows.returning != nil {
			rows.returning.Close()
		}
	}()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := rows.stage(scope.Writes(), b.Ref); err != nil {
		return Result{}, err
	}
	if rows.returning != nil && b.Returning.RegclassOutput && rows.returning.Count != 0 {
		if err := e.captureRegclassOutput(scope.op, rows.returning); err != nil {
			return Result{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	complete = true
	result := Result{Count: int64(len(rows.actions))}
	if rows.returning != nil {
		result.Rows = rows.returning
	}
	return result, nil
}
