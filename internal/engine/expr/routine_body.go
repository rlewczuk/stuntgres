package expr

import "stuntgres/internal/sqltype"

// RoutineBodyInputs contains nonescaping value and retained-source facts needed
// at actual entry. No caller argument buffer escapes into an invocation.
type RoutineBodyInputs struct {
	HasNull       bool
	Count         int
	First, Second *sqltype.Descriptor
	// LiteralNullDurationPairs identifies only OVERLAPS pairs whose retained
	// source contains a literal NULL, so the strict addition is absent.
	// Runtime NULL values do not supply this initialization-elision fact.
	LiteralNullDurationPairs uint8
	// These are the existing immutable binder callsite coordinates, not an
	// execution receipt. The actual init owner belongs only to its command.
	Occurrence uint32
	Source     FoldSourceOccurrence
	// AggregateOwner is the original owning declaration, not an SQL actor
	// override. The executor and host authenticate exact support membership.
	AggregateOwner RoutineReference
}
