package engine

// Retained physical prefixes and already-produced view rows are immutable data.
// No expression producer, bound frame or finished StatementScope crosses the
// eager public boundary, including a direct column projection of a lazy view.
func detachQuerySource(source *querySource) {
	if source == nil {
		return
	}
	for _, child := range source.sources {
		if child != source {
			detachQuerySource(child)
		}
	}
	if source.input != nil {
		detachQuerySource(source.input)
	}
	if source.nestedStage != nil {
		source.nestedStage.Close()
		source.nestedStage = nil
	}
	source.nestedRoot = nil
	if source.lineage != nil {
		stage, inputs := source.lineage.stage, source.lineage.inputs
		source.lineage = nil
		stage.close()
		if inputs != stage {
			inputs.close()
		}
		source.nestedInputs = nil
	} else if source.nestedInputs != nil {
		source.nestedInputs.close()
		source.nestedInputs = nil
	}
	source.host, source.locks = nil, nil
	source.pgSequences = nil
	source.authority = nil
	source.routineRecipe, source.routineChecks = nil, nil
	source.versions = nil
	source.joinLeft, source.joinRight, source.joinPairs = nil, nil, nil
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
