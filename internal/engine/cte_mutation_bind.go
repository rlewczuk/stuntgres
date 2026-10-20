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
		b.Ref, err = binder.Host.AdmittedReference(auth, s.Name, requiredRef)
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
		binder.Target.InsertTarget = b.Relation
		if binder.Target.RuleNamespace != nil {
			// Reserve a mutation lexical frame without exposing INSERT's target.
			binder.Query.Frame = 1
			// SELECT frame allocation stays monotonic across stored modifiers.
		}
		if binder.Host.ReplayInsertAuthority() && len(b.Relation.ColumnMeta.Identities) != 0 && s.Override != parser.OverrideSystem {
			return b, sqlError("0A000", "restore identity rows require OVERRIDING SYSTEM VALUE")
		}
		if !b.Relation.VirtualView {
			b.Storage = binder.Host.AuthorizationStorage(auth, b.Ref.Address)
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
		if binder.Host.RelationHasRules(b.Relation) {
			return b, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`cannot execute MERGE on relation "%s"`, b.Relation.Name()), Detail: "MERGE is not supported for relations with rules."}
		}
	case *parser.Delete:
		var err error
		b.Ref, err = binder.Host.AdmittedReference(auth, s.Name, requiredRef)
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
			b.Storage = binder.Host.AuthorizationStorage(auth, b.Ref.Address)
			if b.Storage == nil && b.Relation.RelationKind == catalog.TableRelation {
				return b, fmt.Errorf("stuntgres: relation storage is missing")
			}
		}
		binder.Target.Relation, binder.Target.Ref = b.Relation, b.Ref
		if s.CurrentOf != nil {
			b.CurrentOf = s.CurrentOf.Text
		}
		binder.Target.deleteTarget, binder.Target.Name, binder.Target.alias = true, b.Relation.Name(), s.Alias.Text
		binder.Query.Auth = auth
		if s.Using != nil {
			target := binder.mutationTargetNamespace()
			binder.Query.Hidden = append(binder.Query.Hidden, target.Qualifiers[0])
			var sources queryNamespace
			binder.Target.deleteUsingSources = true
			b.Delete.Using, sources, err = binder.BindFrom(s.Using)
			binder.Target.deleteUsingSources = false
			if err != nil {
				return b, err
			}
			b.Delete.SourceWidth = len(sources.Frame)
			target, err = combineQueryNamespace(target, rebaseQueryNamespace(sources, len(b.Relation.Columns)))
			if err != nil {
				return b, err
			}
			binder.Query.Namespace = &target
			binder.Clause.AggregateClause = "DELETE"
		}
		if s.Where != nil {
			b.Predicate, err = binder.Bind(s.Where, expressionPolicy{Context: FilterContext})
			if err != nil {
				return b, err
			}
		}
		if s.Returning != nil {
			b.Returning, err = binder.BindReturning(s.Returning, ReturningDelete)
			if err != nil {
				return b, err
			}
		}
	case *parser.Update:
		var err error
		b.Ref, err = binder.Host.AdmittedReference(auth, s.Name, requiredRef)
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
			b.Storage = binder.Host.AuthorizationStorage(auth, b.Ref.Address)
			if b.Storage == nil && b.Relation.RelationKind == catalog.TableRelation {
				return b, fmt.Errorf("stuntgres: relation storage is missing")
			}
		}
		binder.Query.Auth = auth
		if err = bindUpdate(&b, binder, s); err != nil {
			return b, err
		}
		b.QuerySources = binder.Query.Sources
	}
	b.UsedColumns = binder.Target.Uses
	b.SubqueryDemand = binder.Query.subqueryDemand
	b.VisibilitySnapshot = binder.Consumer.VisibilitySnapshot
	if b.Relation != nil {
		switch statement.(type) {
		case *parser.Insert, *parser.Update, *parser.Merge:
			if err := binder.CaptureRetainedWriteInput(nil, b.Relation.ColumnMeta.Generations, b.Relation.Constraints.Check); err != nil {
				return b, err
			}
		}
	}
	b.QuerySources = binder.Query.Sources
	if binder.Target.RuleNamespace == nil {
		if err := binder.Host.BindViewWrite(&b, auth, statement); err != nil {
			return b, err
		}
	}
	binder.Query.Sources = b.QuerySources
	if b.ViewWrite != nil {
		if err := binder.CaptureRetainedWriteInput(b.Insert.Defaults, b.ViewWrite.Base().ColumnMeta.Generations, b.ViewWrite.Base().Constraints.Check); err != nil {
			return b, err
		}
	}
	// Stored-view grafts add real source/visibility demands outside the scalar
	// binder. Retain them in both this command and the statement accumulators.
	binder.Query.subqueryDemand = binder.Query.subqueryDemand || b.SubqueryDemand
	binder.Consumer.VisibilitySnapshot = binder.Consumer.VisibilitySnapshot || b.VisibilitySnapshot
	b.SubqueryDemand = binder.Query.subqueryDemand
	b.VisibilitySnapshot = binder.Consumer.VisibilitySnapshot
	return b, nil
}

// Only auxiliary commands need save/restore; ordinary DML keeps its existing
// inline binding path. Lexical CTEs and all statement accumulators are shared.
func (b *expressionBinder) bindCTEMutation(statement parser.Statement) (binding, error) {
	relation, ref, insertTarget := b.Target.Relation, b.Target.Ref, b.Target.InsertTarget
	updateParameters, updateInputs := b.Params.UpdateParameters, b.Params.updateInputParameters
	deleteTarget, deleteSources := b.Target.deleteTarget, b.Target.deleteUsingSources
	mergeWhen, mergeInput := b.Target.mergeWhen, b.Target.mergeInput
	targetName, targetAlias, uses := b.Target.Name, b.Target.alias, b.Target.Uses
	returningRead := b.Target.returningReadRequired
	subqueries, visibility := b.Query.subqueryDemand, b.Consumer.VisibilitySnapshot
	singleInsert := b.Clause.stringSingleInsert
	restoreQuery := b.EnterQuery(true)
	defer func() {
		restoreQuery()
		b.Target.Relation, b.Target.Ref, b.Target.InsertTarget = relation, ref, insertTarget
		b.Params.UpdateParameters, b.Params.updateInputParameters = updateParameters, updateInputs
		b.Target.deleteTarget, b.Target.deleteUsingSources = deleteTarget, deleteSources
		b.Target.mergeWhen, b.Target.mergeInput = mergeWhen, mergeInput
		b.Target.Name, b.Target.alias, b.Target.Uses = targetName, targetAlias, uses
		b.Target.returningReadRequired = returningRead
		b.Query.subqueryDemand, b.Consumer.VisibilitySnapshot = subqueries, visibility
		b.Clause.stringSingleInsert = singleInsert
	}()
	b.Target.Relation, b.Target.Ref, b.Target.InsertTarget = nil, ids.ObjectRef{}, nil
	b.Params.UpdateParameters, b.Params.updateInputParameters = false, nil
	b.Target.deleteTarget, b.Target.deleteUsingSources, b.Target.mergeWhen, b.Target.mergeInput = false, false, false, false
	b.Target.Name, b.Target.alias, b.Target.Uses = "", "", nil
	b.Target.returningReadRequired, b.Query.subqueryDemand, b.Consumer.VisibilitySnapshot = false, false, false
	b.Clause.stringSingleInsert = false
	b.Query.Frame = 0 // Installed mutation/RETURNING row frames are frame 1.
	oldCTEs := len(b.cte.bindings)
	defer func() { b.cte.bindings = b.cte.bindings[:oldCTEs] }()
	if definitions := parser.StatementWith(statement); len(definitions) != 0 {
		if _, err := b.bindCTEDefinitions(definitions, parser.StatementWithRecursive(statement)); err != nil {
			return binding{}, err
		}
	}
	var required ids.ObjectRef
	if b.Query.lockAdmission != nil {
		for _, target := range b.Query.lockAdmission.MutationTargets {
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
		required, err = b.Host.ResolveReference(b.Query.Auth, name)
		if err != nil {
			return binding{}, err
		}
	}
	return bindMutationCommand(b, b.Query.Auth, required, statement)
}
