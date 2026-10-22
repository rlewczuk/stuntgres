package engine

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Each Execute has a newly analyzed binding. Preflight mutates only that private
// binding, never a stored source/default definition or held prepared statement.
func preflightMerge(c *exec.Context, merge *analyze.MergeBinding, catalog catalog.Read) error {
	owner := *c
	owner.Scope, owner.Frame = 1, 1
	var err error
	merge.Source, err = view.PreflightQuery(&owner, merge.Source)
	if err != nil {
		return err
	}
	if err := view.ValidateQueryJoinExecution(&owner, merge.Source, catalog, 0); err != nil {
		return err
	}
	merge.On, _, err = exec.PreflightCoercions(&owner, merge.On)
	if err != nil {
		return err
	}
	if err := view.ValidateExpressionJoinExecution(&owner, merge.On, catalog); err != nil {
		return err
	}
	for i := range merge.Arms {
		arm := &merge.Arms[i]
		arm.Condition, _, err = exec.PreflightCoercions(&owner, arm.Condition)
		if err != nil {
			return err
		}
		if err := view.ValidateExpressionJoinExecution(&owner, arm.Condition, catalog); err != nil {
			return err
		}
		if arm.Action == parser.MergeUpdateAction {
			arm.Input.Update, err = preflightUpdate(&owner, arm.Input.Update)
			if err != nil {
				return err
			}
			if err := view.ValidateMutationJoinExecution(&owner, &arm.Input, nil, catalog); err != nil {
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
			folded, _, err := exec.PreflightCoercions(&owner, expression)
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
			analyze.ApplyDefaultOrigin(view.BindingDefaultOrigin(&arm.Input, j), &defaultOwner)
			arm.Input.Insert.Defaults[j], _, err = exec.PreflightCoercionsWithPolicy(&defaultOwner, expression, true)
			if err != nil {
				return err
			}
		}
		if err := view.ValidateMutationJoinExecution(&owner, &arm.Input, arm.Input.Expressions, catalog); err != nil {
			return err
		}
	}
	if merge.HasBySource && merge.HasByTarget {
		var facts analyze.FullJoinFacts
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
				facts.Truth, err = dml.MergeOnTruth(value)
				facts.Constant = true
			}
		} else {
			facts, err = analyze.FullJoinCondition(&owner, &plan.JoinNode{LeftWidth: merge.TargetWidth, RightWidth: merge.SourceWidth}, merge.On, false)
		}
		if err != nil {
			return err
		}
		if !facts.Constant && len(facts.Keys) == 0 {
			return sqlError("0A000", "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions")
		}
	}
	return nil
}

// executeMerge runs the captured MERGE through dml.ExecuteMerge; the engine
// tests call it.
func executeMerge(ctx context.Context, scope *StatementScope, b analyze.Binding, target, inputs *exec.Source, parameters []sqltype.Value, schema string) (Result, error) {
	outcome, err := dml.ExecuteMerge(scope.writeHost(), scope.mergeInput(ctx, b, target, inputs, parameters, schema))
	if err != nil {
		return Result{}, err
	}
	return outcomeResult(outcome), nil
}

// mergeInput is a MERGE execution over the statement's comment catalog,
// captured under Engine.mu before the dispatch released it.
func (s *StatementScope) mergeInput(ctx context.Context, b analyze.Binding, target, inputs *exec.Source, parameters []sqltype.Value, schema string) dml.MergeInput {
	return dml.MergeInput{Context: ctx, Binding: b, Target: target, Inputs: inputs, Parameters: parameters, Schema: schema, Catalog: s.inquiry.commentCatalog}
}
