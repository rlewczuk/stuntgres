package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The existing command binders share a single statement expression binder.
// Parameter finishing, dependencies and source occurrence ordinals stay outer.
func (e *Engine) bindMutationCommand(binder *expressionBinder, auth AuthorizationView, requiredRef ids.ObjectRef, statement parser.Statement) (binding, error) {
	b := binding{}
	op := binder.op
	switch s := statement.(type) {
	case *parser.Insert:
		var err error
		b.ref, err = e.admittedReference(auth, s.Name, requiredRef)
		if err != nil {
			return b, err
		}
		if b.ref != requiredRef {
			return b, ids.ErrCatalogReplaced
		}
		b.relation = auth.Catalog.MustRelation(b.ref.Address)
		if b.relation.RelationKind == catalog.CompositeRelation {
			return b, typedef.CompositeOpenError(b.relation, s.Name.Parts[0].Pos)
		}
		if b.relation.RelationKind == catalog.IndexRelation {
			return b, tableKind(b.relation, "insert into")
		}
		binder.target.insertTarget = b.relation
		if binder.target.ruleNamespace != nil {
			// Reserve a mutation lexical frame without exposing INSERT's target.
			binder.query.frame = 1
			// SELECT frame allocation stays monotonic across stored modifiers.
		}
		if op != nil && op.replayOrZero().insertAuthority && len(b.relation.ColumnMeta.Identities) != 0 && s.Override != parser.OverrideSystem {
			return b, sqlError("0A000", "restore identity rows require OVERRIDING SYSTEM VALUE")
		}
		if !b.relation.VirtualView {
			b.storage = e.authorizationStorageLocked(auth, b.ref.Address)
			if b.storage == nil && b.relation.RelationKind == catalog.TableRelation {
				return b, fmt.Errorf("stuntgres: relation storage is missing")
			}
		}
		if err := e.bindInsertInput(&b, binder, auth, s); err != nil {
			return b, err
		}
		if err := e.bindInsertConflict(&b, binder, auth, s.Conflict); err != nil {
			return b, err
		}
		if err := e.bindInsertConflictUpdate(&b, binder, s); err != nil {
			return b, err
		}
		if err := e.bindInsertReturning(&b, binder, auth, s); err != nil {
			return b, err
		}
	case *parser.Merge:
		if err := e.bindMerge(&b, binder, auth, requiredRef, s); err != nil {
			return b, err
		}
		if len(rulesOf(b.relation)) != 0 {
			return b, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`cannot execute MERGE on relation "%s"`, b.relation.Name()), Detail: "MERGE is not supported for relations with rules."}
		}
	case *parser.Delete:
		var err error
		b.ref, err = e.admittedReference(auth, s.Name, requiredRef)
		if err != nil {
			return b, err
		}
		if b.ref != requiredRef {
			return b, ids.ErrCatalogReplaced
		}
		b.relation = auth.Catalog.MustRelation(b.ref.Address)
		if b.relation.RelationKind == catalog.CompositeRelation {
			return b, typedef.CompositeOpenError(b.relation, s.Name.Parts[0].Pos)
		}
		if b.relation.RelationKind == catalog.IndexRelation {
			return b, tableKind(b.relation, "delete from")
		}
		if !b.relation.VirtualView {
			b.storage = e.authorizationStorageLocked(auth, b.ref.Address)
			if b.storage == nil && b.relation.RelationKind == catalog.TableRelation {
				return b, fmt.Errorf("stuntgres: relation storage is missing")
			}
		}
		binder.target.relation, binder.target.ref = b.relation, b.ref
		if s.CurrentOf != nil {
			b.currentOf = s.CurrentOf.Text
		}
		binder.target.deleteTarget, binder.target.name, binder.target.alias = true, b.relation.Name(), s.Alias.Text
		binder.query.auth = &auth
		if s.Using != nil {
			target := binder.mutationTargetNamespace()
			binder.query.hidden = append(binder.query.hidden, target.qualifiers[0])
			var sources queryNamespace
			binder.target.deleteUsingSources = true
			b.delete.using, sources, err = binder.bindFrom(s.Using)
			binder.target.deleteUsingSources = false
			if err != nil {
				return b, err
			}
			b.delete.sourceWidth = len(sources.frame)
			target, err = combineQueryNamespace(target, rebaseQueryNamespace(sources, len(b.relation.Columns)))
			if err != nil {
				return b, err
			}
			binder.query.namespace = &target
			binder.clause.aggregateClause = "DELETE"
		}
		if s.Where != nil {
			b.predicate, err = binder.bind(s.Where, expressionPolicy{context: filterContext})
			if err != nil {
				return b, err
			}
		}
		if s.Returning != nil {
			b.returning, err = binder.bindReturning(s.Returning, returningDelete)
			if err != nil {
				return b, err
			}
		}
	case *parser.Update:
		var err error
		b.ref, err = e.admittedReference(auth, s.Name, requiredRef)
		if err != nil {
			return b, err
		}
		if b.ref != requiredRef {
			return b, ids.ErrCatalogReplaced
		}
		b.relation = auth.Catalog.MustRelation(b.ref.Address)
		if b.relation.RelationKind == catalog.CompositeRelation {
			return b, typedef.CompositeOpenError(b.relation, s.Name.Parts[0].Pos)
		}
		if b.relation.RelationKind == catalog.IndexRelation {
			return b, tableKind(b.relation, "change")
		}
		if !b.relation.VirtualView && b.relation.RelationKind == catalog.CatalogRelation && !guardedCatalogRelation(b.relation) {
			return b, tableKind(b.relation, "change")
		}
		if !b.relation.VirtualView {
			b.storage = e.authorizationStorageLocked(auth, b.ref.Address)
			if b.storage == nil && b.relation.RelationKind == catalog.TableRelation {
				return b, fmt.Errorf("stuntgres: relation storage is missing")
			}
		}
		binder.query.auth = &auth
		if err = e.bindUpdate(&b, binder, s); err != nil {
			return b, err
		}
		b.querySources = binder.query.sources
	}
	b.usedColumns = binder.target.uses
	b.subqueryDemand = binder.query.subqueryDemand
	b.visibilitySnapshot = binder.consumer.visibilitySnapshot
	if b.relation != nil {
		switch statement.(type) {
		case *parser.Insert, *parser.Update, *parser.Merge:
			if err := binder.captureRetainedWriteInput(nil, b.relation.ColumnMeta.Generations, b.relation.Constraints.Check); err != nil {
				return b, err
			}
		}
	}
	b.querySources = binder.query.sources
	if binder.target.ruleNamespace == nil {
		if err := e.bindViewWrite(&b, auth, statement); err != nil {
			return b, err
		}
	}
	binder.query.sources = b.querySources
	if b.viewWrite != nil {
		if err := binder.captureRetainedWriteInput(b.insert.defaults, b.viewWrite.base.ColumnMeta.Generations, b.viewWrite.base.Constraints.Check); err != nil {
			return b, err
		}
	}
	// Stored-view grafts add real source/visibility demands outside the scalar
	// binder. Retain them in both this command and the statement accumulators.
	binder.query.subqueryDemand = binder.query.subqueryDemand || b.subqueryDemand
	binder.consumer.visibilitySnapshot = binder.consumer.visibilitySnapshot || b.visibilitySnapshot
	b.subqueryDemand = binder.query.subqueryDemand
	b.visibilitySnapshot = binder.consumer.visibilitySnapshot
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
		for _, target := range b.query.lockAdmission.mutationTargets {
			if target.statement == statement {
				required = target.ref
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
		required, err = b.engine.resolveReference(*b.query.auth, name)
		if err != nil {
			return binding{}, err
		}
	}
	return b.engine.bindMutationCommand(b, *b.query.auth, required, statement)
}
