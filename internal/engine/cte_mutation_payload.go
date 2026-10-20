package engine

func (*binding) cteMutationPayload() {}

// mutationBinding recovers the analyzed binding of a data-modifying CTE.
func mutationBinding(d *cteDefinition) (*binding, bool) {
	b, ok := d.mutation.(*binding)
	return b, ok && b != nil
}
