package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Engine.mu is held. Both ordinary and materialized views freeze the same bound
// identities and renderer metadata; only ordinary views execute this definition
// when read. A zero rule requests a new identity for a newly created relation.
func (e *Engine) freezeViewDefinitionLocked(auth AuthorizationView, syntax *parser.Select, b binding, barrier, invoker bool, rule ids.OID, kind catalog.RelationKind, temporary bool) (*viewDefinition, error) {
	temporaryMessage := "temporary views are unsupported"
	if kind == catalog.MaterializedViewRelation {
		temporaryMessage = "materialized views must not use temporary tables or views"
	}
	temporaryConsumer := kind == catalog.ViewRelation && temporary
	if rule == 0 {
		var err error
		rule, err = e.catalog.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
	}
	return e.freezeQueryPresentationLocked(auth, syntax, b, barrier, invoker, rule, temporaryMessage, temporaryConsumer)
}

// Policies share analyzed expression/query presentation without inheriting a
// view's temporary-source restriction. An empty message permits those sources.
func (e *Engine) freezeQueryPresentationLocked(auth AuthorizationView, syntax *parser.Select, b binding, barrier, invoker bool, rule ids.OID, temporaryMessage string, temporaryConsumer bool) (*viewDefinition, error) {
	dependencies, err := frozenQueryDependencies(auth.Catalog, b)
	if err != nil {
		return nil, err
	}
	definition := &viewDefinition{query: b.query, syntax: syntax, dependencies: dependencies, barrier: barrier, invoker: invoker, rule: rule}
	definition.literals, definition.castUsages = b.capture.literals, b.capture.castUsages
	definition.routineRefs = b.capture.routineRefs
	definition.operatorRefs = b.capture.operatorRefs
	definition.composites = b.capture.composites
	definition.similarSubstrings = b.capture.similarSubstrings
	definition.posixSubstrings = b.capture.posixSubstrings
	definition.collations = b.capture.collations
	definition.columns, definition.stars = b.capture.columns, b.capture.stars
	definition.frames, definition.queries = collectQueryFrames(b.query.Root)
	definition.namespaces = b.capture.namespaces
	definition.sequenceRefs = make(map[int]ids.ObjectRef, len(b.prepared.sequenceConstants))
	for _, constant := range b.prepared.sequenceConstants {
		if constant.temporary != (ids.SessionToken{}) && temporaryMessage != "" && (!temporaryConsumer || auth.session == nil || constant.temporary != auth.session.token) {
			return nil, sqlError("0A000", temporaryMessage)
		}
		definition.sequenceRefs[constant.offset] = constant.ref
		if _, exists := auth.Catalog.Find(constant.ref.Address); exists {
			definition.dependencies = append(definition.dependencies, catalog.ExpressionDependency{Ref: constant.ref, Revision: constant.stamp})
		}
	}
	for _, constant := range b.prepared.regtypeConstants {
		if constant.value.Type() != nil {
			var references []ids.ObjectRef
			if err := (scalarRenderer{dependencies: &references}).recordCatalogAliasDependencies(auth.Catalog, constant.value); err != nil {
				return nil, err
			}
			if err := (scalarRenderer{dependencies: &references}).recordRoutineReferences(auth.Catalog, constant.value); err != nil {
				return nil, err
			}
			for _, ref := range references {
				definition.dependencies = append(definition.dependencies, catalog.ExpressionDependency{Ref: ref, Revision: auth.Catalog.Get(ref.Address).Revision()})
			}
		}
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(constant.id)}
		if constant.operator != 0 {
			address.ClassID = ids.OperatorClassID
		}
		if d, exists := auth.Catalog.Find(address); exists {
			definition.dependencies = append(definition.dependencies, catalog.ExpressionDependency{Ref: e.catalog.Domain.Ref(auth.Catalog.Epoch(), address), Revision: d.Revision()})
		}
	}
	definition.sources = make([]viewLeaf, len(b.querySources))
	for occurrence, source := range b.querySources {
		leaf := &definition.sources[occurrence]
		leaf.ref, leaf.mark, leaf.only = source.ref, source.mark, source.only
		leaf.analysisOnly = source.analysisOnly
		leaf.attributes = append([]int32(nil), source.relation.Attributes...)
		for _, slot := range source.uses {
			leaf.reads = append(leaf.reads, source.relation.Attributes[slot])
		}
		if ns := auth.Catalog.MustNamespace(source.relation.Namespace()); ns.Temporary != (ids.SessionToken{}) && temporaryMessage != "" && (!temporaryConsumer || auth.session == nil || ns.Temporary != auth.session.token) {
			return nil, sqlError("0A000", temporaryMessage)
		}
	}
	if temporaryConsumer {
		definition.temporary = auth.session.token
		definition.references = make(map[ids.ObjectRef]catalog.Descriptor, len(definition.sources)+len(definition.dependencies))
		capture := func(ref ids.ObjectRef) {
			address := ref.Address
			address.SubID = 0
			definition.references[ref] = auth.Catalog.Get(address)
		}
		for _, leaf := range definition.sources {
			capture(leaf.ref)
		}
		for _, dependency := range definition.dependencies {
			capture(dependency.Ref)
		}
	}
	return definition, nil
}
