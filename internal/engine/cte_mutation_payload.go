package engine

// cteMutation is the opaque payload a data-modifying CTE definition carries.
// Plan code only tests it for presence; the engine recovers the analyzed
// statement binding through mutationBinding. A definition never stores a
// typed nil, so a nil payload keeps meaning "a query CTE".
type cteMutation interface{ cteMutationPayload() }

func (*binding) cteMutationPayload() {}

// mutationBinding recovers the analyzed binding of a data-modifying CTE.
func mutationBinding(d *cteDefinition) (*binding, bool) {
	b, ok := d.mutation.(*binding)
	return b, ok && b != nil
}
