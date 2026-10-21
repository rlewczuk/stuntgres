package stored

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Policies share analyzed expression/query presentation without inheriting a
// view's temporary-source restriction. An empty message permits those sources.
// The session token is the consumer's TEMP owner, or zero without a session;
// rule is passed as is, never allocated.
func FreezeQueryPresentation(view catalog.Read, session ids.SessionToken, syntax *parser.Select, b analyze.Binding, barrier, invoker bool, rule ids.OID, temporaryMessage string, temporaryConsumer bool) (*View, error) {
	dependencies, err := frozenQueryDependencies(view, b)
	if err != nil {
		return nil, err
	}
	definition := &View{query: b.Query, syntax: syntax, dependencies: dependencies, barrier: barrier, invoker: invoker, rule: rule}
	definition.literals, definition.castUsages = b.Capture.Literals, b.Capture.CastUsages
	definition.routineRefs = b.Capture.RoutineRefs
	definition.operatorRefs = b.Capture.OperatorRefs
	definition.composites = b.Capture.Composites
	definition.similarSubstrings = b.Capture.SimilarSubstrings
	definition.posixSubstrings = b.Capture.PosixSubstrings
	definition.collations = b.Capture.Collations
	definition.columns, definition.stars = b.Capture.Columns, b.Capture.Stars
	definition.frames, definition.queries = analyze.CollectQueryFrames(b.Query.Root)
	definition.namespaces = b.Capture.Namespaces
	definition.sequenceRefs = make(map[int]ids.ObjectRef, len(b.Prepared.SequenceConstants))
	for _, constant := range b.Prepared.SequenceConstants {
		if constant.Temporary != (ids.SessionToken{}) && temporaryMessage != "" && (!temporaryConsumer || constant.Temporary != session) {
			return nil, sqlError("0A000", temporaryMessage)
		}
		definition.sequenceRefs[constant.Offset] = constant.Ref
		if _, exists := view.Find(constant.Ref.Address); exists {
			definition.dependencies = append(definition.dependencies, catalog.ExpressionDependency{Ref: constant.Ref, Revision: constant.Stamp})
		}
	}
	for _, constant := range b.Prepared.RegtypeConstants {
		if constant.Value.Type() != nil {
			var references []ids.ObjectRef
			if err := (ScalarRenderer{Dependencies: &references}).RecordCatalogAliasDependencies(view, constant.Value); err != nil {
				return nil, err
			}
			if err := (ScalarRenderer{Dependencies: &references}).RecordRoutineReferences(view, constant.Value); err != nil {
				return nil, err
			}
			for _, ref := range references {
				definition.dependencies = append(definition.dependencies, catalog.ExpressionDependency{Ref: ref, Revision: view.Get(ref.Address).Revision()})
			}
		}
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(constant.ID)}
		if constant.Operator != 0 {
			address.ClassID = ids.OperatorClassID
		}
		if d, exists := view.Find(address); exists {
			definition.dependencies = append(definition.dependencies, catalog.ExpressionDependency{Ref: view.Owner().Domain.Ref(view.Epoch(), address), Revision: d.Revision()})
		}
	}
	definition.sources = make([]ViewLeaf, len(b.QuerySources))
	for occurrence, source := range b.QuerySources {
		leaf := &definition.sources[occurrence]
		leaf.Ref, leaf.Mark, leaf.Only = source.Ref, source.Mark, source.Only
		leaf.AnalysisOnly = source.AnalysisOnly
		leaf.Attributes = append([]int32(nil), source.Relation.Attributes...)
		for _, slot := range source.Uses {
			leaf.Reads = append(leaf.Reads, source.Relation.Attributes[slot])
		}
		if ns := view.MustNamespace(source.Relation.Namespace()); ns.Temporary != (ids.SessionToken{}) && temporaryMessage != "" && (!temporaryConsumer || ns.Temporary != session) {
			return nil, sqlError("0A000", temporaryMessage)
		}
	}
	if temporaryConsumer {
		definition.temporary = session
		definition.references = make(map[ids.ObjectRef]catalog.Descriptor, len(definition.sources)+len(definition.dependencies))
		capture := func(ref ids.ObjectRef) {
			address := ref.Address
			address.SubID = 0
			definition.references[ref] = view.Get(address)
		}
		for _, leaf := range definition.sources {
			capture(leaf.Ref)
		}
		for _, dependency := range definition.dependencies {
			capture(dependency.Ref)
		}
	}
	return definition, nil
}

// Preserve the prepared source manifest. Only frozen presentation loses a
// constructor's erased outer identity, and only when no executable occurrence
// still needs that identity. The ordinary pg_depend expression owner decides.
func frozenQueryDependencies(view catalog.Read, b analyze.Binding) ([]catalog.ExpressionDependency, error) {
	var erased map[sqltype.ID]bool
	plan.VisitCTEQuery(b.Query.Root, func(plan.Node) {}, func(bound expr.Expr) {
		plan.WalkExpression(bound, func(node expr.Expr) {
			if n, ok := node.(plan.SubqueryExpr); ok && n.ErasedLeftType != 0 {
				if erased == nil {
					erased = make(map[sqltype.ID]bool)
				}
				erased[n.ErasedLeftType] = true
			}
		})
	}, true)
	if len(erased) == 0 {
		return b.Dependencies, nil
	}
	refs := DependRefs{Context: context.Background(), Catalog: view}
	refs.Query(b.Query.Root)
	if refs.Err != nil {
		return nil, refs.Err
	}
	dependencies := make([]catalog.ExpressionDependency, 0, len(b.Dependencies))
	for _, dependency := range b.Dependencies {
		address := dependency.Ref.Address
		if address.ClassID == ids.TypeClassID && erased[sqltype.ID(address.ObjectID)] {
			if _, retained := refs.Addresses[address]; !retained {
				continue
			}
		}
		dependencies = append(dependencies, dependency)
	}
	return dependencies, nil
}
