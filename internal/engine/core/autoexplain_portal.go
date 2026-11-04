package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
)

// A Bind owns the live accepted SELECT graph, but neither samples nor creates
// an observer. First Execute captures current settings against that same graph.
func (s *StatementScope) startAutoExplainPortal(p *protocolPortal) error {
	if p.autoExplainStatement == nil {
		return nil
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	previousSource := s.statementStatsSource
	if p.Entry.Source != "" {
		s.statementStatsSource = p.Entry.Source
	}
	defer func() { s.statementStatsSource = previousSource }()
	f, err := s.beginAutoExplainLocked(p.autoExplainStatement)
	p.autoExplainFamily = s.autoExplainFamily
	if err != nil || f == nil {
		return err
	}
	if err := e.captureAutoExplainParametersLocked(s, f, p.producer.state.Parameters); err != nil {
		return err
	}
	var observation *exec.Observation
	if f.options.Analyze {
		_, observation = exec.Observe(s.executionContext(), f.options.Timing)
	} else {
		_, observation = exec.ObservePresentation(s.executionContext())
	}
	observation.SetLogicalMetrics(f.buffers, f.wal, f.triggers)
	observation.SetForeignOptions(catalog.ForeignExplainOptions{Costs: true, Verbose: f.options.Verbose}, f.options.Structured())
	f.observation = observation
	execution := preparedCommandExecution{binding: p.autoExplainBinding, queryRoot: p.producer.Root, expressions: p.autoExplainBinding.Expressions, auth: p.producer.routineAuth}
	renderer, tree, err := e.captureExecutedPlanLocked(s, p.autoExplainStatement, &execution, observation, f.options, nil, nil)
	if err != nil {
		s.autoExplain = nil
		f.finish(nil, false)
		return err
	}
	renderer.SuppressActual = !f.options.Analyze
	renderer.Commands = &f.renderers
	if tree != nil {
		tree.Actual = nil
	}
	f.renderers = append(f.renderers, renderer)
	p.autoExplain, s.autoExplain = f, nil
	return nil
}
