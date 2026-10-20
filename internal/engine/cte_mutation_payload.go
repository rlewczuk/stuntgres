package engine

func (*binding) CTEMutationPayload() {}

// mutationBinding recovers the analyzed binding of a data-modifying CTE.
func mutationBinding(d *cteDefinition) (*binding, bool) {
	b, ok := d.Mutation.(*binding)
	return b, ok && b != nil
}
