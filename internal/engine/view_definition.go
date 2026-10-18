package engine

import "stuntgres/internal/parser"

// Engine.mu is held. Both ordinary and materialized views freeze the same bound
// identities and renderer metadata; only ordinary views execute this definition
// when read. A zero rule requests a new identity for a newly created relation.
func (e *Engine) freezeViewDefinitionLocked(auth AuthorizationView, syntax *parser.Select, b binding, barrier, invoker bool, rule OID, kind relationKind, temporary bool) (*viewDefinition, error) {
	temporaryMessage := "temporary views are unsupported"
	if kind == materializedViewRelation {
		temporaryMessage = "materialized views must not use temporary tables or views"
	}
	temporaryConsumer := kind == viewRelation && temporary
	if rule == 0 {
		var err error
		rule, err = e.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
	}
	return e.freezeQueryPresentationLocked(auth, syntax, b, barrier, invoker, rule, temporaryMessage, temporaryConsumer)
}

// Policies share analyzed expression/query presentation without inheriting a
// view's temporary-source restriction. An empty message permits those sources.
func (e *Engine) freezeQueryPresentationLocked(auth AuthorizationView, syntax *parser.Select, b binding, barrier, invoker bool, rule OID, temporaryMessage string, temporaryConsumer bool) (*viewDefinition, error) {
	dependencies, err := frozenQueryDependencies(auth.catalog, b)
	if err != nil {
		return nil, err
	}
	definition := &viewDefinition{query: b.query, syntax: syntax, dependencies: dependencies, barrier: barrier, invoker: invoker, rule: rule}
	definition.literals, definition.castUsages = b.viewLiterals, b.viewCastUsages
	definition.intaggRefs = b.viewIntaggRefs
	definition.operatorRefs = b.viewOperatorRefs
	definition.composites = b.viewComposites
	definition.similarSubstrings = b.viewSimilarSubstrings
	definition.posixSubstrings = b.viewPosixSubstrings
	definition.collations = b.viewCollations
	definition.columns, definition.stars = b.viewColumns, b.viewStars
	definition.frames, definition.queries = collectQueryFrames(b.query.Root)
	definition.namespaces = b.viewNamespaces
	definition.sequenceRefs = make(map[int]ObjectRef, len(b.sequenceConstants))
	for _, constant := range b.sequenceConstants {
		if constant.temporary != (SessionToken{}) && temporaryMessage != "" && (!temporaryConsumer || auth.session == nil || constant.temporary != auth.session.token) {
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
			if err := (scalarRenderer{dependencies: &references}).recordCatalogAliasDependencies(auth.catalog, constant.value); err != nil {
				return nil, err
			}
			if err := (scalarRenderer{dependencies: &references}).recordRoutineReferences(auth.catalog, constant.value); err != nil {
				return nil, err
			}
			for _, ref := range references {
				definition.dependencies = append(definition.dependencies, expressionDependency{ref: ref, revision: auth.catalog.state.descriptors[ref.Address].Revision()})
			}
		}
		address := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(constant.id)}
		if constant.operator != 0 {
			address.ClassID = OperatorClassID
		}
		if d, exists := auth.catalog.state.descriptors[address]; exists {
			definition.dependencies = append(definition.dependencies, expressionDependency{ref: ObjectRef{owner: e.domain, Epoch: auth.catalog.Epoch(), Address: address}, revision: d.Revision()})
		}
	}
	definition.sources = make([]viewLeaf, len(b.querySources))
	for occurrence, source := range b.querySources {
		leaf := &definition.sources[occurrence]
		leaf.ref, leaf.mark, leaf.only = source.ref, source.mark, source.only
		leaf.analysisOnly = source.analysisOnly
		leaf.attributes = append([]int32(nil), source.relation.attributes...)
		for _, slot := range source.uses {
			leaf.reads = append(leaf.reads, source.relation.attributes[slot])
		}
		if ns := auth.catalog.state.descriptors[source.relation.namespace].(*namespaceDescriptor); ns.temporary != (SessionToken{}) && temporaryMessage != "" && (!temporaryConsumer || auth.session == nil || ns.temporary != auth.session.token) {
			return nil, sqlError("0A000", temporaryMessage)
		}
	}
	if temporaryConsumer {
		definition.temporary = auth.session.token
		definition.references = make(map[ObjectRef]Descriptor, len(definition.sources)+len(definition.dependencies))
		capture := func(ref ObjectRef) {
			address := ref.Address
			address.SubID = 0
			definition.references[ref] = auth.catalog.state.descriptors[address]
		}
		for _, leaf := range definition.sources {
			capture(leaf.ref)
		}
		for _, dependency := range definition.dependencies {
			capture(dependency.ref)
		}
	}
	return definition, nil
}
