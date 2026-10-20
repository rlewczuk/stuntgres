package engine

// Retained physical prefixes and already-produced view rows are immutable data.
// No expression producer, bound frame or finished StatementScope crosses the
// eager public boundary, including a direct column projection of a lazy view.
func detachQuerySource(source *querySource) {
	if source == nil {
		return
	}
	for _, child := range source.Sources {
		if child != source {
			detachQuerySource(child)
		}
	}
	if source.Input != nil {
		detachQuerySource(source.Input)
	}
	if source.NestedStage != nil {
		source.NestedStage.Close()
		source.NestedStage = nil
	}
	source.NestedRoot = nil
	if source.Lineage != nil {
		stage, inputs := source.Lineage.Stage, source.Lineage.Inputs
		source.Lineage = nil
		stage.Close()
		if inputs != stage {
			inputs.Close()
		}
		source.NestedInputs = nil
	} else if source.NestedInputs != nil {
		source.NestedInputs.Close()
		source.NestedInputs = nil
	}
	source.Host, source.Locks = nil, nil
	source.PGSequences = nil
	source.Authority = nil
	source.RoutineRecipe, source.RoutineChecks = nil, nil
	source.Versions = nil
	source.JoinLeft, source.JoinRight, source.JoinPairs = nil, nil, nil
	// Dependent outputs share the left source and consecutive right invocation
	// sources; detach each once rather than traversing a graph per padded row.
	var left, right *querySource
	for _, lineage := range source.invocationLineage {
		if lineage.left != left {
			detachQuerySource(lineage.left)
			left = lineage.left
		}
		if lineage.right != right {
			detachQuerySource(lineage.right)
			right = lineage.right
		}
	}
	source.invocationLineage = nil
}
