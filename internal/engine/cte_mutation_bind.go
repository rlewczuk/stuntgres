package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The existing command binders share a single statement expression binder.
// Parameter finishing, dependencies and source occurrence ordinals stay outer.
func bindMutationCommand(binder *expressionBinder, auth analyze.Authority, requiredRef ids.ObjectRef, statement parser.Statement) (binding, error) {
	b := binding{}
	switch s := statement.(type) {
	case *parser.Insert:
		var err error
		b.Ref, err = binder.host.AdmittedReference(auth, s.Name, requiredRef)
		if err != nil {
			return b, err
		}
		if b.Ref != requiredRef {
			return b, ids.ErrCatalogReplaced
		}
		b.Relation = auth.ReadCatalog().MustRelation(b.Ref.Address)
		if b.Relation.RelationKind == catalog.CompositeRelation {
			return b, typedef.CompositeOpenError(b.Relation, s.Name.Parts[0].Pos)
		}
		if b.Relation.RelationKind == catalog.IndexRelation {
			return b, analyze.TableKindError(b.Relation, "insert into")
		}
		binder.target.insertTarget = b.Relation
		if binder.target.ruleNamespace != nil {
			// Reserve a mutation lexical frame without exposing INSERT's target.
			binder.query.frame = 1
			// SELECT frame allocation stays monotonic across stored modifiers.
		}
		if binder.host.ReplayInsertAuthority() && len(b.Relation.ColumnMeta.Identities) != 0 && s.Override != parser.OverrideSystem {
			return b, sqlError("0A000", "restore identity rows require OVERRIDING SYSTEM VALUE")
		}
		if !b.Relation.VirtualView {
			b.Storage = binder.host.AuthorizationStorage(auth, b.Ref.Address)
			if b.Storage == nil && b.Relation.RelationKind == catalog.TableRelation {
				return b, fmt.Errorf("stuntgres: relation storage is missing")
			}
		}
		if err := bindInsertInput(&b, binder, auth, s); err != nil {
			return b, err
		}
		if err := bindInsertConflict(&b, binder, auth, s.Conflict); err != nil {
			return b, err
		}
		if err := bindInsertConflictUpdate(&b, binder, s); err != nil {
			return b, err
		}
		if err := bindInsertReturning(&b, binder, auth, s); err != nil {
			return b, err
		}
	case *parser.Merge:
		if err := bindMerge(&b, binder, auth, requiredRef, s); err != nil {
			return b, err
		}
		if binder.host.RelationHasRules(b.Relation) {
			return b, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`cannot execute MERGE on relation "%s"`, b.Relation.Name()), Detail: "MERGE is not supported for relations with rules."}
		}
	case *parser.Delete:
		var err error
		b.Ref, err = binder.host.AdmittedReference(auth, s.Name, requiredRef)
		if err != nil {
			return b, err
		}
		if b.Ref != requiredRef {
			return b, ids.ErrCatalogReplaced
		}
		b.Relation = auth.ReadCatalog().MustRelation(b.Ref.Address)
		if b.Relation.RelationKind == catalog.CompositeRelation {
			return b, typedef.CompositeOpenError(b.Relation, s.Name.Parts[0].Pos)
		}
		if b.Relation.RelationKind == catalog.IndexRelation {
			return b, analyze.TableKindError(b.Relation, "delete from")
		}
		if !b.Relation.VirtualView {
			b.Storage = binder.host.AuthorizationStorage(auth, b.Ref.Address)
			if b.Storage == nil && b.Relation.RelationKind == catalog.TableRelation {
				return b, fmt.Errorf("stuntgres: relation storage is missing")
			}
		}
		binder.target.relation, binder.target.ref = b.Relation, b.Ref
		if s.CurrentOf != nil {
			b.CurrentOf = s.CurrentOf.Text
		}
		binder.target.deleteTarget, binder.target.name, binder.target.alias = true, b.Relation.Name(), s.Alias.Text
		binder.query.auth = auth
		if s.Using != nil {
			target := binder.mutationTargetNamespace()
			binder.query.hidden = append(binder.query.hidden, target.qualifiers[0])
			var sources queryNamespace
			binder.target.deleteUsingSources = true
			b.Delete.Using, sources, err = binder.bindFrom(s.Using)
			binder.target.deleteUsingSources = false
			if err != nil {
				return b, err
			}
			b.Delete.SourceWidth = len(sources.frame)
			target, err = combineQueryNamespace(target, rebaseQueryNamespace(sources, len(b.Relation.Columns)))
			if err != nil {
				return b, err
			}
			binder.query.namespace = &target
			binder.clause.aggregateClause = "DELETE"
		}
		if s.Where != nil {
			b.Predicate, err = binder.bind(s.Where, expressionPolicy{context: filterContext})
			if err != nil {
				return b, err
			}
		}
		if s.Returning != nil {
			b.Returning, err = binder.bindReturning(s.Returning, returningDelete)
			if err != nil {
				return b, err
			}
		}
	case *parser.Update:
		var err error
		b.Ref, err = binder.host.AdmittedReference(auth, s.Name, requiredRef)
		if err != nil {
			return b, err
		}
		if b.Ref != requiredRef {
			return b, ids.ErrCatalogReplaced
		}
		b.Relation = auth.ReadCatalog().MustRelation(b.Ref.Address)
		if b.Relation.RelationKind == catalog.CompositeRelation {
			return b, typedef.CompositeOpenError(b.Relation, s.Name.Parts[0].Pos)
		}
		if b.Relation.RelationKind == catalog.IndexRelation {
			return b, analyze.TableKindError(b.Relation, "change")
		}
		if !b.Relation.VirtualView && b.Relation.RelationKind == catalog.CatalogRelation && !analyze.GuardedCatalogRelation(b.Relation) {
			return b, analyze.TableKindError(b.Relation, "change")
		}
		if !b.Relation.VirtualView {
			b.Storage = binder.host.AuthorizationStorage(auth, b.Ref.Address)
			if b.Storage == nil && b.Relation.RelationKind == catalog.TableRelation {
				return b, fmt.Errorf("stuntgres: relation storage is missing")
			}
		}
		binder.query.auth = auth
		if err = bindUpdate(&b, binder, s); err != nil {
			return b, err
		}
		b.QuerySources = binder.query.sources
	}
	b.UsedColumns = binder.target.uses
	b.SubqueryDemand = binder.query.subqueryDemand
	b.VisibilitySnapshot = binder.consumer.visibilitySnapshot
	if b.Relation != nil {
		switch statement.(type) {
		case *parser.Insert, *parser.Update, *parser.Merge:
			if err := binder.captureRetainedWriteInput(nil, b.Relation.ColumnMeta.Generations, b.Relation.Constraints.Check); err != nil {
				return b, err
			}
		}
	}
	b.QuerySources = binder.query.sources
	if binder.target.ruleNamespace == nil {
		if err := binder.host.BindViewWrite(&b, auth, statement); err != nil {
			return b, err
		}
	}
	binder.query.sources = b.QuerySources
	if b.ViewWrite != nil {
		if err := binder.captureRetainedWriteInput(b.Insert.Defaults, b.ViewWrite.Base().ColumnMeta.Generations, b.ViewWrite.Base().Constraints.Check); err != nil {
			return b, err
		}
	}
	// Stored-view grafts add real source/visibility demands outside the scalar
	// binder. Retain them in both this command and the statement accumulators.
	binder.query.subqueryDemand = binder.query.subqueryDemand || b.SubqueryDemand
	binder.consumer.visibilitySnapshot = binder.consumer.visibilitySnapshot || b.VisibilitySnapshot
	b.SubqueryDemand = binder.query.subqueryDemand
	b.VisibilitySnapshot = binder.consumer.visibilitySnapshot
	return b, nil
}

// Only auxiliary commands need save/restore; ordinary DML keeps its existing
// inline binding path. Lexical CTEs and all statement accumulators are shared.
func (b *expressionBinder) bindCTEMutation(statement parser.Statement) (binding, error) {
	relation, ref, insertTarget := b.target.relation, b.target.ref, b.target.insertTarget
	updateParameters, updateInputs := b.params.updateParameters, b.params.updateInputParameters
	deleteTarget, deleteSources := b.target.deleteTarget, b.target.deleteUsingSources
	mergeWhen, mergeInput := b.target.mergeWhen, b.target.mergeInput
	targetName, targetAlias, uses := b.target.name, b.target.alias, b.target.uses
	returningRead := b.target.returningReadRequired
	subqueries, visibility := b.query.subqueryDemand, b.consumer.visibilitySnapshot
	singleInsert := b.clause.stringSingleInsert
	restoreQuery := b.enterQuery(true)
	defer func() {
		restoreQuery()
		b.target.relation, b.target.ref, b.target.insertTarget = relation, ref, insertTarget
		b.params.updateParameters, b.params.updateInputParameters = updateParameters, updateInputs
		b.target.deleteTarget, b.target.deleteUsingSources = deleteTarget, deleteSources
		b.target.mergeWhen, b.target.mergeInput = mergeWhen, mergeInput
		b.target.name, b.target.alias, b.target.uses = targetName, targetAlias, uses
		b.target.returningReadRequired = returningRead
		b.query.subqueryDemand, b.consumer.visibilitySnapshot = subqueries, visibility
		b.clause.stringSingleInsert = singleInsert
	}()
	b.target.relation, b.target.ref, b.target.insertTarget = nil, ids.ObjectRef{}, nil
	b.params.updateParameters, b.params.updateInputParameters = false, nil
	b.target.deleteTarget, b.target.deleteUsingSources, b.target.mergeWhen, b.target.mergeInput = false, false, false, false
	b.target.name, b.target.alias, b.target.uses = "", "", nil
	b.target.returningReadRequired, b.query.subqueryDemand, b.consumer.visibilitySnapshot = false, false, false
	b.clause.stringSingleInsert = false
	b.query.frame = 0 // Installed mutation/RETURNING row frames are frame 1.
	oldCTEs := len(b.cte.bindings)
	defer func() { b.cte.bindings = b.cte.bindings[:oldCTEs] }()
	if definitions := parser.StatementWith(statement); len(definitions) != 0 {
		if _, err := b.bindCTEDefinitions(definitions, parser.StatementWithRecursive(statement)); err != nil {
			return binding{}, err
		}
	}
	var required ids.ObjectRef
	if b.query.lockAdmission != nil {
		for _, target := range b.query.lockAdmission.MutationTargets {
			if target.Statement == statement {
				required = target.Ref
				break
			}
		}
	}
	if required.Domain() == nil {
		var name parser.ObjectName
		switch s := statement.(type) {
		case *parser.Insert:
			name = s.Name
		case *parser.Update:
			name = s.Name
		case *parser.Delete:
			name = s.Name
		case *parser.Merge:
			name = s.Name
		}
		var err error
		required, err = b.host.ResolveReference(b.query.auth, name)
		if err != nil {
			return binding{}, err
		}
	}
	return bindMutationCommand(b, b.query.auth, required, statement)
}
