package catalog

// OwnedAggregate carries the actual definition. Helpers are populated only by
// intagg's lookup-only extension aliases; ordinary aggregates use real SFUNC
// descriptors and leave Helpers empty.
type OwnedAggregate struct {
	Definition AggregateDefinition
	Helpers    [2]RoutineIdentity
}
