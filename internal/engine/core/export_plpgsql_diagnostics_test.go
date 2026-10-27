package core

import (
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
)

// DiagnosticsLiveEntry is a detached test snapshot, never a runtime registry.
type DiagnosticsLiveEntry struct {
	Node, Parent any
	Definition   *proc.Definition
	Frame        *proc.PLFrame
	Origin       proc.PLOrigin
	SQLName      string
	Query        string
	Statement    int
}

func ScopeDiagnosticsLive(s *StatementScope) []DiagnosticsLiveEntry {
	var entries []DiagnosticsLiveEntry
	for n := s.routineLive; n != nil; n = n.parent {
		e := DiagnosticsLiveEntry{Node: n, Parent: n.parent, Definition: n.definition, Origin: n.origin, SQLName: n.sqlName, Query: n.query, Statement: n.statement}
		if n.host != nil {
			e.Frame = n.host.run.Frame
		}
		entries = append(entries, e)
	}
	return entries
}

func ScopeDiagnosticsExpressionSites(s *StatementScope) []*parser.Select {
	var sites []*parser.Select
	for n := s.expressionLive; n != nil; n = n.parent {
		sites = append(sites, n.site)
	}
	return sites
}

func ScopeDiagnosticsContext(s *StatementScope) string { return s.currentPLContext() }

// Seed distinguishing enclosing state; actual nested entries are still installed
// exclusively by real invocation/evaluator/SQL owners.
func ScopeDiagnosticsEnclosing(s *StatementScope, definition *proc.Definition, site *parser.Select) func() {
	beforeRoutine, beforeExpression := s.routineLive, s.expressionLive
	s.routineLive = &routineLive{parent: beforeRoutine, definition: definition}
	s.expressionLive = &plExpressionLive{parent: beforeExpression, site: site}
	return func() { s.routineLive, s.expressionLive = beforeRoutine, beforeExpression }
}
