package core

import (
	"context"
	"fmt"
	"time"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type explainAdmission struct {
	options explainOptions
	err error
}

type explainExecution struct {
	options explainOptions
	renderers []*explainRenderer
	started time.Time
	planning time.Duration
	executing time.Time
	statement parser.Statement
	expected *Description
}

// captureExplainCommandLocked borrows the real prepared execution graph, after
// canonical binding and preflight and before any executor startup. Presentation
// is frozen now; rendering after execution only reads scalar measurements.
func (e *Engine) captureExplainCommandLocked(scope *StatementScope, statement parser.Statement, execution *preparedCommandExecution, observation *exec.Observation) (*explainTree, error) {
	owner := scope.explain
	if owner == nil { return nil, nil }
	binding := execution.binding
	binding.Expressions = execution.expressions
	observation.Register(execution.queryRoot)
	if binding.Update != nil {
		observation.Register(binding.Update.From)
	}
	observation.Register(binding.Delete.Using)
	if binding.Merge != nil {
		observation.Register(binding.Merge.Source)
	}
	if execution.queryRoot != nil && binding.Query != nil {
		query := *binding.Query
		query.Root = execution.queryRoot
		binding.Query = &query
	}
	renderer, err := newExplainRenderer(scope, execution.auth, owner.options)
	if err != nil { return nil, err }
	renderer.observation = observation
	if _, err = renderer.command(statement, explainBound{Binding: binding}); err != nil { return nil, err }
	if scope.ruleAction.Auth != nil && len(renderer.trees) != 0 {
		renderer.trees[0].details = append([]string{"Rule rewrite command"}, renderer.trees[0].details...)
	}
	owner.renderers = append(owner.renderers, renderer)
	if statement == owner.statement {
		copy(owner.renderers[1:], owner.renderers[:len(owner.renderers)-1])
		owner.renderers[0] = renderer
	}
	if owner.executing.IsZero() && owner.options.summary {
		owner.planning = time.Since(owner.started)
		owner.executing = time.Now()
	}
	if len(renderer.trees) == 0 { return nil, nil }
	tree := renderer.trees[0]
	tree.actual = &exec.Actual{Loops: 1}
	return tree, nil
}

func (e *Engine) executeExplainAnalyze(scope *StatementScope, statement *parser.Explain, count int, importFrame FrameImporter, options explainOptions) (Result, error) {
	owner := &explainExecution{options: options}
	owner.statement, owner.expected = statement.Statement, scope.Expected
	if options.summary { owner.started = time.Now() }
	ctx := scope.executionContext()
	previous, invocation := scope.explain, scope.invocationContext
	scope.explain, scope.invocationContext = owner, ctx
	defer func() { scope.explain, scope.invocationContext = previous, invocation }()
	result, err := e.executeDataStatement(scope, statement.Statement, count, importFrame)
	if err != nil {
		if result.Rows != nil { _ = result.Rows.Close() }
		return Result{}, err
	}
	if result.Rows != nil {
		if err := consumeExplainRows(ctx, result.Rows); err != nil { return Result{}, err }
	}
	var execution time.Duration
	if options.summary && !owner.executing.IsZero() { execution = time.Since(owner.executing) }
	var lines []string
	for _, renderer := range owner.renderers { lines = append(lines, renderer.render()...) }
	if options.summary {
		lines = append(lines, fmt.Sprintf("Planning Time: %.3f ms", float64(owner.planning)/1e6))
		lines = append(lines, fmt.Sprintf("Execution Time: %.3f ms", float64(execution)/1e6))
	}
	e.Mu.Lock()
	schema := e.explainSchemaLocked()
	e.Mu.Unlock()
	if len(lines) == 0 {
		lines = append(lines, "Rewrite: nothing")
	}
	rows := make([][]sqltype.Value, len(lines))
	for i, line := range lines { rows[i] = []sqltype.Value{sqltype.String(schema[0].Usage.Type, line)} }
	if err := scope.beginProtocolSimplePortal(); err != nil { return Result{}, err }
	output, err := anonymousResult(schema, rows, 0)
	output.CommandTag = "EXPLAIN"
	return output, err
}

// Existing child finalizers already evaluate output. Pulling their detached
// rows does not evaluate again, and retained producers use the same owner when
// present. Closing participates in the ordinary statement failure boundary.
func consumeExplainRows(ctx context.Context, rows exec.RowResult) (failure error) {
	defer func() { if err := rows.Close(); failure == nil { failure = err } }()
	values := make([]sqltype.Value, len(rows.Schema()))
	for {
		present, err := rows.Next(ctx, values)
		if err != nil { return err }
		if !present { return nil }
	}
}

func (s *StatementScope) explainWrapper(statement parser.Statement) bool {
	return s.explain != nil && s.explain.statement == statement && s.explain.expected == s.Expected
}
