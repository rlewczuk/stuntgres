package engine

import "stuntgres/internal/engine/plan"

func (*binding) CTEMutationPayload() {}

// mutationBinding recovers the analyzed binding of a data-modifying CTE.
func mutationBinding(d *plan.CTEDefinition) (*binding, bool) {
	b, ok := d.Mutation.(*binding)
	return b, ok && b != nil
}
