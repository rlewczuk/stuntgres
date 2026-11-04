package core

import (
	"context"
	"fmt"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core/explain"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
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
	wrapper   *parser.Explain
}

// captureExplainCommandLocked borrows the real prepared execution graph, after
// canonical binding and preflight and before any executor startup. Presentation
// is frozen now; rendering after execution only reads scalar measurements.
func (e *Engine) captureExplainCommandLocked(scope *StatementScope, statement parser.Statement, execution *preparedCommandExecution, observation *exec.Observation, cteOwner *exec.CTEInvocation, cteDefinition *plan.CTEDefinition) (*explain.Tree, error) {
	owner := scope.explain
	if owner == nil {
		return nil, nil
	}
	renderer, tree, err := e.captureExecutedPlanLocked(scope, statement, execution, observation, owner.options, cteOwner, cteDefinition)
	if err != nil {
		return nil, err
	}
	renderer.Commands = &owner.renderers
	owner.renderers = append(owner.renderers, renderer)
	if statement == owner.statement {
		copy(owner.renderers[1:], owner.renderers[:len(owner.renderers)-1])
		owner.renderers[0] = renderer
	}
	if owner.executing.IsZero() && owner.options.Summary {
		owner.planning = time.Since(owner.started)
		owner.executing = time.Now()
	}
	return tree, nil
}

// Both explicit ANALYZE and the automatic hook borrow this actual accepted
// execution, including current expressions and provider-selection identity.
func (e *Engine) captureExecutedPlanLocked(scope *StatementScope, statement parser.Statement, execution *preparedCommandExecution, observation *exec.Observation, options explain.Options, cteOwner *exec.CTEInvocation, cteDefinition *plan.CTEDefinition) (*explain.Renderer, *explain.Tree, error) {
	binding := execution.binding
	binding.Expressions = execution.expressions
	if err := reserveExecutedCTEs(observation, scope.cteInvocation, binding); err != nil {
		return nil, nil, err
	}
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
	renderer, err := newExplainRenderer(scope, execution.auth, options)
	if err != nil {
		return nil, nil, err
	}
	renderer.Observation = observation
	renderer.DML = exec.DMLObservationOf(scope.executionContext())
	renderer.CTEs = scope.cteInvocation
	renderer.CTEOwner, renderer.CTEDefinition = cteOwner, cteDefinition
	if _, err = renderer.Command(statement, explain.Bound{Binding: binding}); err != nil {
		return nil, nil, err
	}
	if scope.ruleAction.Auth != nil {
		renderer.MarkRuleRewrite()
	}
	tree := renderer.CommandTree()
	if tree == nil {
		return renderer, nil, nil
	}
	if options.Analyze {
		if tree.Actual == nil {
			tree.Actual = &exec.Actual{}
		}
		tree.Actual.Loops = 1
	}
	return renderer, tree, nil
}

// Only actual accepted invocation owners reserve mutation measurements. Lazy
// CTEs may be displayed before demand enters them, so their exact owner's
// record is reserved now and populated at the existing child handoff.
func reserveExecutedDML(observation *exec.Observation, owner *exec.CTEInvocation, definition *plan.CTEDefinition, statement parser.Statement, binding analyze.Binding) *exec.DMLObservation {
	switch statement.(type) {
	case *parser.Insert:
		if binding.Query == nil {
			return observation.ReserveDML(owner, definition, exec.DMLInsertInput)
		}
	case *parser.Merge:
		return observation.ReserveDML(owner, definition, exec.DMLMergeTarget, exec.DMLMergeCandidates)
	case *parser.Update, *parser.Delete:
		var roles [4]exec.DMLRole
		roles[0] = exec.DMLTargetScan
		count := 1
		if binding.Update != nil && binding.Update.From != nil || binding.Delete.Using != nil {
			roles[count], count = exec.DMLCandidateJoin, count+1
		}
		predicate := binding.Predicate
		if binding.Update != nil {
			predicate = binding.Update.Predicate
		}
		if predicate != nil {
			roles[count], count = exec.DMLPredicate, count+1
		}
		if binding.CurrentOf != "" {
			roles[count], count = exec.DMLCursorLocator, count+1
		}
		return observation.ReserveDML(owner, definition, roles[:count]...)
	default:
		return nil
	}
	return observation.ReserveDML(owner, definition)
}

func reserveExecutedCTEs(observation *exec.Observation, owners *exec.CTEInvocation, binding analyze.Binding) error {
	if observation == nil {
		return nil
	}
	for _, definition := range binding.CTEs {
		mutation, ok := analyze.MutationBinding(definition)
		if !ok {
			continue
		}
		owner := owners
		for owner != nil && owner.Frame != definition.Owner {
			owner = owner.Parent
		}
		if owner == nil {
			return fmt.Errorf("stuntgres: missing EXPLAIN CTE invocation owner")
		}
		statement, command := definition.Statement, *mutation
		if rewrite := view.RuleCTEsOf(binding.Rule.CTEs)[definition]; rewrite != nil {
			statement, command = rewrite.Command.Syntax, rewrite.Command.Binding
		}
		reserveExecutedDML(observation, owner, definition, statement, command)
		if err := reserveExecutedCTEs(observation, owners, command); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) executeExplainAnalyze(scope *StatementScope, statement *parser.Explain, count int, importFrame FrameImporter, options explain.Options) (Result, error) {
	owner := &explainExecution{options: options}
	owner.statement, owner.expected = statement.Statement, scope.Expected
	owner.wrapper = statement
	if options.Summary {
		owner.started = time.Now()
	}
	ctx := scope.executionContext()
	previous, invocation := scope.explain, scope.invocationContext
	scope.explain, scope.invocationContext = owner, ctx
	defer func() { scope.explain, scope.invocationContext = previous, invocation }()
	defer func() { owner.renderers = nil }()
	result, err := e.executeDataStatement(scope, statement.Statement, count, importFrame)
	if frame := scope.statementStats; frame != nil && !frame.utility {
		scope.statementStatsReturned(&result, err)
	}
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
	if frame := scope.statementStats; frame != nil && !frame.utility {
		scope.captureStatementStatsTerminal(frame)
		frame.finish(true)
		scope.statementStats = nil
	}
	var execution time.Duration
	if options.Summary && !owner.executing.IsZero() {
		execution = time.Since(owner.executing)
	}
	if err := explain.ResolveCommands(owner.renderers); err != nil {
		return Result{}, err
	}
	var lines []string
	if !options.Structured() {
		for _, renderer := range owner.renderers {
			lines = append(lines, renderer.Render()...)
		}
	}
	var summaries explain.Summaries
	if options.Summary && (!options.Structured() || !owner.executing.IsZero()) {
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
