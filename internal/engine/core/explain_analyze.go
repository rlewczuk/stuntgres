package core

import (
	"context"
	"time"

	"stuntgres/internal/engine/core/explain"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type explainAdmission struct {
	options explain.Options
	err     error
}

type explainExecution struct {
	options   explain.Options
	renderers []*explain.Renderer
	started   time.Time
	planning  time.Duration
	executing time.Time
	statement parser.Statement
	expected  *Description
}

// captureExplainCommandLocked borrows the real prepared execution graph, after
// canonical binding and preflight and before any executor startup. Presentation
// is frozen now; rendering after execution only reads scalar measurements.
func (e *Engine) captureExplainCommandLocked(scope *StatementScope, statement parser.Statement, execution *preparedCommandExecution, observation *exec.Observation) (*explain.Tree, error) {
	owner := scope.explain
	if owner == nil {
		return nil, nil
	}
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
	if err != nil {
		return nil, err
	}
	renderer.Observation = observation
	if _, err = renderer.Command(statement, explain.Bound{Binding: binding}); err != nil {
		return nil, err
	}
	if scope.ruleAction.Auth != nil {
		renderer.MarkRuleRewrite()
	}
	owner.renderers = append(owner.renderers, renderer)
	if statement == owner.statement {
		copy(owner.renderers[1:], owner.renderers[:len(owner.renderers)-1])
		owner.renderers[0] = renderer
	}
	if owner.executing.IsZero() && owner.options.Summary {
		owner.planning = time.Since(owner.started)
		owner.executing = time.Now()
	}
	tree := renderer.CommandTree()
	if tree == nil {
		return nil, nil
	}
	tree.Actual = &exec.Actual{Loops: 1}
	return tree, nil
}

func (e *Engine) executeExplainAnalyze(scope *StatementScope, statement *parser.Explain, count int, importFrame FrameImporter, options explain.Options) (Result, error) {
	owner := &explainExecution{options: options}
	owner.statement, owner.expected = statement.Statement, scope.Expected
	if options.Summary {
		owner.started = time.Now()
	}
	ctx := scope.executionContext()
	previous, invocation := scope.explain, scope.invocationContext
	scope.explain, scope.invocationContext = owner, ctx
	defer func() { scope.explain, scope.invocationContext = previous, invocation }()
	result, err := e.executeDataStatement(scope, statement.Statement, count, importFrame)
	if err != nil {
		if result.Rows != nil {
			_ = result.Rows.Close()
		}
		return Result{}, err
	}
	if result.Rows != nil {
		if err := consumeExplainRows(ctx, result.Rows); err != nil {
			return Result{}, err
		}
	}
	var execution time.Duration
	if options.Summary && !owner.executing.IsZero() {
		execution = time.Since(owner.executing)
	}
	var lines []string
	if options.Format != "json" {
		for _, renderer := range owner.renderers {
			lines = append(lines, renderer.Render()...)
		}
	}
	var summaries explain.Summaries
	if options.Summary && (options.Format != "json" || !owner.executing.IsZero()) {
		summaries.Planning, summaries.Execution = &owner.planning, &execution
	}
	e.Mu.Lock()
	schema := e.explainSchemaLocked(options.Format)
	e.Mu.Unlock()
	if len(lines) == 0 {
		lines = append(lines, "Rewrite: nothing")
	}
	rows, err := explain.Output(scope.executionContext(), schema, options, owner.renderers, lines, summaries)
	if err != nil {
		return Result{}, err
	}
	if err := scope.beginProtocolSimplePortal(); err != nil {
		return Result{}, err
	}
	output, err := anonymousResult(schema, rows, 0)
	output.CommandTag = "EXPLAIN"
	return output, err
}

// Existing child finalizers already evaluate output. Pulling their detached
// rows does not evaluate again, and retained producers use the same owner when
// present. Closing participates in the ordinary statement failure boundary.
func consumeExplainRows(ctx context.Context, rows exec.RowResult) (failure error) {
	defer func() {
		if err := rows.Close(); failure == nil {
			failure = err
		}
	}()
	values := make([]sqltype.Value, len(rows.Schema()))
	for {
		present, err := rows.Next(ctx, values)
		if err != nil {
			return err
		}
		if !present {
			return nil
		}
	}
}

func (s *StatementScope) explainWrapper(statement parser.Statement) bool {
	return s.explain != nil && s.explain.statement == statement && s.explain.expected == s.Expected
}
