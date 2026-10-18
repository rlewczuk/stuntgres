package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
		b.relation = auth.catalog.MustRelation(b.ref.Address)
		if b.relation.RelationKind == catalog.CompositeRelation {
			return b, compositeOpenError(b.relation, s.Name.Parts[0].Pos)
		}
		if b.relation.RelationKind == catalog.IndexRelation {
			return b, tableKind(b.relation, "insert into")
		}
		binder.insertTarget = b.relation
		if binder.ruleNamespace != nil {
			// Reserve a mutation lexical frame without exposing INSERT's target.
			binder.queryFrame = 1
			// SELECT frame allocation stays monotonic across stored modifiers.
		}
		if op != nil && op.restoreInsertAuthority && len(b.relation.Identities) != 0 && s.Override != parser.OverrideSystem {
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
		b.relation = auth.catalog.MustRelation(b.ref.Address)
		if b.relation.RelationKind == catalog.CompositeRelation {
			return b, compositeOpenError(b.relation, s.Name.Parts[0].Pos)
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
		binder.relation, binder.ref = b.relation, b.ref
		if s.CurrentOf != nil {
			b.currentOf = s.CurrentOf.Text
		}
		binder.deleteTarget, binder.targetName, binder.targetAlias = true, b.relation.Name(), s.Alias.Text
		binder.queryAuth = &auth
		if s.Using != nil {
			target := binder.mutationTargetNamespace()
			binder.queryHidden = append(binder.queryHidden, target.qualifiers[0])
			var sources queryNamespace
			binder.deleteUsingSources = true
			b.deleteUsing, sources, err = binder.bindFrom(s.Using)
			binder.deleteUsingSources = false
			if err != nil {
				return b, err
			}
			b.deleteSourceWidth = len(sources.frame)
			target, err = combineQueryNamespace(target, rebaseQueryNamespace(sources, len(b.relation.Columns)))
			if err != nil {
				return b, err
			}
			binder.queryNamespace = &target
			binder.aggregateClause = "DELETE"
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
		b.relation = auth.catalog.MustRelation(b.ref.Address)
		if b.relation.RelationKind == catalog.CompositeRelation {
			return b, compositeOpenError(b.relation, s.Name.Parts[0].Pos)
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
		binder.queryAuth = &auth
		if err = e.bindUpdate(&b, binder, s); err != nil {
			return b, err
		}
		b.querySources = binder.querySources
	}
	b.usedColumns = binder.uses
	b.subqueryDemand = binder.subqueryDemand
	b.visibilitySnapshot = binder.visibilitySnapshot
	if b.relation != nil {
		switch statement.(type) {
		case *parser.Insert, *parser.Update, *parser.Merge:
			if err := binder.captureRetainedWriteInput(nil, b.relation.Generations, b.relation.CheckConstraints); err != nil {
				return b, err
			}
		}
	}
	b.querySources = binder.querySources
	if binder.ruleNamespace == nil {
		if err := e.bindViewWrite(&b, auth, statement); err != nil {
			return b, err
		}
	}
	binder.querySources = b.querySources
	if b.viewWrite != nil {
		if err := binder.captureRetainedWriteInput(b.defaults, b.viewWrite.base.Generations, b.viewWrite.base.CheckConstraints); err != nil {
			return b, err
		}
	}
	// Stored-view grafts add real source/visibility demands outside the scalar
	// binder. Retain them in both this command and the statement accumulators.
	binder.subqueryDemand = binder.subqueryDemand || b.subqueryDemand
	binder.visibilitySnapshot = binder.visibilitySnapshot || b.visibilitySnapshot
	b.subqueryDemand = binder.subqueryDemand
	b.visibilitySnapshot = binder.visibilitySnapshot
	return b, nil
}

// Only auxiliary commands need save/restore; ordinary DML keeps its existing
// inline binding path. Lexical CTEs and all statement accumulators are shared.
func (b *expressionBinder) bindCTEMutation(statement parser.Statement) (binding, error) {
	relation, ref, insertTarget := b.relation, b.ref, b.insertTarget
	updateParameters, updateInputs := b.updateParameters, b.updateInputParameters
	deleteTarget, deleteSources := b.deleteTarget, b.deleteUsingSources
	mergeWhen, mergeInput := b.mergeWhen, b.mergeInput
	targetName, targetAlias, uses := b.targetName, b.targetAlias, b.uses
	returningRead := b.returningReadRequired
	subqueries, visibility := b.subqueryDemand, b.visibilitySnapshot
	singleInsert := b.stringSingleInsert
	restoreQuery := b.enterQuery(true)
	defer func() {
		restoreQuery()
		b.relation, b.ref, b.insertTarget = relation, ref, insertTarget
		b.updateParameters, b.updateInputParameters = updateParameters, updateInputs
		b.deleteTarget, b.deleteUsingSources = deleteTarget, deleteSources
		b.mergeWhen, b.mergeInput = mergeWhen, mergeInput
		b.targetName, b.targetAlias, b.uses = targetName, targetAlias, uses
		b.returningReadRequired = returningRead
		b.subqueryDemand, b.visibilitySnapshot = subqueries, visibility
		b.stringSingleInsert = singleInsert
	}()
	b.relation, b.ref, b.insertTarget = nil, ids.ObjectRef{}, nil
	b.updateParameters, b.updateInputParameters = false, nil
	b.deleteTarget, b.deleteUsingSources, b.mergeWhen, b.mergeInput = false, false, false, false
	b.targetName, b.targetAlias, b.uses = "", "", nil
	b.returningReadRequired, b.subqueryDemand, b.visibilitySnapshot = false, false, false
	b.stringSingleInsert = false
	b.queryFrame = 0 // Installed mutation/RETURNING row frames are frame 1.
	oldCTEs := len(b.cteBindings)
	defer func() { b.cteBindings = b.cteBindings[:oldCTEs] }()
	if definitions := parser.StatementWith(statement); len(definitions) != 0 {
		if _, err := b.bindCTEDefinitions(definitions, parser.StatementWithRecursive(statement)); err != nil {
			return binding{}, err
		}
	}
	var required ids.ObjectRef
	if b.queryLockAdmission != nil {
		for _, target := range b.queryLockAdmission.mutationTargets {
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
		required, err = b.engine.resolveReference(*b.queryAuth, name)
		if err != nil {
			return binding{}, err
		}
	}
	return b.engine.bindMutationCommand(b, *b.queryAuth, required, statement)
}
