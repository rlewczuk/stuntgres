package catalog

// The actual installed aggregate owns these lookup-only aliases. They are not
// descriptors, extension members, executable functions, or pg_proc SQL rows.
type OwnedAggregate struct {
	Definition AggregateDefinition
	Helpers    [2]RoutineIdentity
}
