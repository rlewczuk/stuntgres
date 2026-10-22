package engine

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

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
