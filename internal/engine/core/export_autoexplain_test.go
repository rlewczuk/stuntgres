package core

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
)

// Borrow actual invocation artifacts without creating a replacement graph.
func AutoExplainWitnessForTest(s *StatementScope) (*exec.Observation, []plan.Node, func() bool) {
	f := s.autoExplain
	if f == nil {
		return nil, nil, func() bool { return s.autoExplain == nil }
	}
	var nodes []plan.Node
	for _, r := range f.renderers {
		nodes = append(nodes, r.PresentedNodes()...)
	}
	return f.observation, nodes, func() bool {
		return f.finished && f.renderers == nil && f.observation == nil && f.parameters == "" && f.source == "" && f.context == "" && f.primary == nil
	}
}
func AutoExplainActiveForTest(s *StatementScope) bool       { return s.autoExplain != nil }
func AutoExplainPortalActiveForTest(p *protocolPortal) bool { return p.autoExplain != nil }

func AutoExplainPortalRetirementForTest(p *protocolPortal) func() bool {
	f := p.autoExplain
	return func() bool {
		return f != nil && f.finished && f.observation == nil && f.renderers == nil &&
			f.source == "" && f.parameters == "" && p.autoExplain == nil
	}
}
