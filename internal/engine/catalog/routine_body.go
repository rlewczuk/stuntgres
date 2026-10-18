package catalog

// These are declaration identities, never permission results or a SQL evaluator.
// All edges come from REL_18_0 system_functions.sql/pg_proc.dat/pg_cast.dat.
// A descriptor borrows one engine-owned backing array; original binding/recipe
// manifests separately retain every actually selected edge's scoped lifetime.
type RoutineBodyDemand uint8

const (
	BodyAlways RoutineBodyDemand = iota
	BodyBooleanText
	BodyNetworkText
	BodyLeftDuration
	BodyRightDuration
)

type RoutineBodyEdge struct {
	Routine *Procedure
	Demand  RoutineBodyDemand
}
