package engine

import "stuntgres/internal/parser"

// Engine.mu is held. Both ordinary and materialized views freeze the same bound
// identities and renderer metadata; only ordinary views execute this definition
// when read. A zero rule requests a new identity for a newly created relation.
func (e *Engine) freezeViewDefinitionLocked(auth AuthorizationView, syntax *parser.Select, b binding, barrier, invoker bool, rule OID, kind relationKind) (*viewDefinition, error) {
	temporaryMessage := "temporary views are unsupported"
	if kind == materializedViewRelation {
		temporaryMessage = "materialized views must not use temporary tables or views"
	}
	if rule == 0 {
		var err error
		rule, err = e.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
	}
	return e.freezeQueryPresentationLocked(auth, syntax, b, barrier, invoker, rule, temporaryMessage)
}

// Policies share analyzed expression/query presentation without inheriting a
// view's temporary-source restriction. An empty message permits those sources.
func (e *Engine) freezeQueryPresentationLocked(auth AuthorizationView, syntax *parser.Select, b binding, barrier, invoker bool, rule OID, temporaryMessage string) (*viewDefinition, error) {
	definition := &viewDefinition{query: b.query, syntax: syntax, dependencies: b.dependencies, barrier: barrier, invoker: invoker, rule: rule}
	definition.literals, definition.castUsages = b.viewLiterals, b.viewCastUsages
	definition.similarSubstrings = b.viewSimilarSubstrings
	definition.collations = b.viewCollations
	definition.columns, definition.stars = b.viewColumns, b.viewStars
	definition.frames, definition.queries = collectQueryFrames(b.query.Root)
	definition.namespaces = b.viewNamespaces
	definition.sequenceRefs = make(map[int]ObjectRef, len(b.sequenceConstants))
	for _, constant := range b.sequenceConstants {
		if constant.temporary != (SessionToken{}) && temporaryMessage != "" {
			return nil, sqlError("0A000", temporaryMessage)
		}
		definition.sequenceRefs[constant.offset] = constant.ref
		if _, exists := auth.catalog.state.descriptors[constant.ref.Address]; exists {
			definition.dependencies = append(definition.dependencies, expressionDependency{ref: constant.ref, revision: constant.stamp})
		}
	}
	for _, constant := range b.regtypeConstants {
		if constant.value.Type() != nil {
			var references []ObjectRef
			if err := (scalarRenderer{dependencies: &references}).recordRegtypeReferences(auth.catalog, constant.value); err != nil {
				return nil, err
			}
			for _, ref := range references {
				definition.dependencies = append(definition.dependencies, expressionDependency{ref: ref, revision: auth.catalog.state.descriptors[ref.Address].Revision()})
			}
		}
		address := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(constant.id)}
		if d, exists := auth.catalog.state.descriptors[address]; exists {
			definition.dependencies = append(definition.dependencies, expressionDependency{ref: ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: address}, revision: d.Revision()})
		}
	}
	definition.sources = make([]viewLeaf, len(b.querySources))
	for occurrence, source := range b.querySources {
		leaf := &definition.sources[occurrence]
		leaf.ref, leaf.mark = source.ref, source.mark
		leaf.attributes = append([]int32(nil), source.relation.attributes...)
		for _, slot := range source.uses {
			leaf.reads = append(leaf.reads, source.relation.attributes[slot])
		}
		if ns := auth.catalog.state.descriptors[source.relation.namespace].(*namespaceDescriptor); ns.temporary != (SessionToken{}) && temporaryMessage != "" {
			return nil, sqlError("0A000", temporaryMessage)
		}
	}
	return definition, nil
}
