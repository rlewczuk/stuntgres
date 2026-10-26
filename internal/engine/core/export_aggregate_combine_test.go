package core

import "context"

// ScopeAggregateSQLGate returns the actual statement-owned SQL gate so external
// composed tests can retain its token and observe a canceled production waiter.
func ScopeAggregateSQLGate(s *StatementScope) chan struct{} {
	s.aggregateGateOnce.Do(func() { s.aggregateGate = make(chan struct{}, 1) })
	return s.aggregateGate
}

// ScopeAggregateInvocationContext observes restoration of the worker context.
func ScopeAggregateInvocationContext(s *StatementScope) context.Context {
	return s.invocationContext
}
