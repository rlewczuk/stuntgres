package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/plan"
)

// cteMutationOf recovers a definition's binding for test assertions.
func cteMutationOf(definition *plan.CTEDefinition) *analyze.Binding {
	mutation, _ := analyze.MutationBinding(definition)
	return mutation
}
