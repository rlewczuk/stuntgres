package engine

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Engine.mu is held on entry and released on every return. Dependency waits
// and provider callbacks run without it; one stage publishes the entire closure.
func (e *Engine) alterSchemaLocked(scope *StatementScope, op *Operation, auth AuthorizationView, s *parser.AlterTableSetSchema, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	ref, err := e.resolveRelationReference(auth, s.Name, true)
	if err == nil && ref != scope.lockedRef {
		return fail(ids.ErrCatalogReplaced)
	}
	if s.IfExists && renameMissing(err) {
		op.session.mu.Lock()
		err = publicationOperationLocked(e, op)
		op.session.mu.Unlock()
		if err == nil {
			err = op.Context().Err()
		}
		if err != nil {
			return fail(err)
		}
		scope.completed = true
		e.mu.Unlock()
		op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, s.Name.Parts[len(s.Name.Parts)-1].Text)})
		return Result{}, nil
	}
	if err != nil {
		return fail(err)
	}
	if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	old := scope.lockedDescriptor.(*relationDescriptor)
	if old.RelationKind != tableRelation && old.RelationKind != viewRelation && old.RelationKind != materializedViewRelation && old.RelationKind != foreignRelation {
		return fail(sqlError("0A000", "ALTER TABLE SET SCHEMA supports ordinary tables only"))
	}
	destination, err := e.creationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Schema, {Text: old.name}}})
	if err != nil {
		return fail(err)
	}
	if err = checkPersistentCreationNamespace(auth.catalog, destination.Address); err != nil {
		return fail(err)
	}
	if err = auth.CheckCreate(destination); err != nil {
		return fail(err)
	}
	if auth.catalog.MustNamespace(old.namespace).Temporary != (ids.SessionToken{}) {
		return fail(sqlError("0A000", "cannot move objects into or out of temporary schemas"))
	}
	if destination.Address == old.namespace {
		op.session.mu.Lock()
		err = publicationOperationLocked(e, op)
		op.session.mu.Unlock()
		if err == nil {
			err = op.Context().Err()
		}
		if err != nil {
			return fail(err)
		}
		scope.completed = true
		e.mu.Unlock()
		return Result{}, nil
	}
	owned := make([]*relationDescriptor, 0)
	for _, d := range auth.catalog.Descriptors() {
		r, ok := d.(*relationDescriptor)
		if ok && r.SQLIndex != nil && r.SQLIndex.Parent == old.address {
			owned = append(owned, r)
			continue
		}
		if !ok || r.RelationKind != sequenceRelation {
			continue
		}
		a := r.Sequence.Owned
		if old.RelationKind == tableRelation && a.ClassID == old.address.ClassID && a.ObjectID == old.address.ObjectID && old.hasAttribute(a.SubID) {
			owned = append(owned, r)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].address.ObjectID < owned[j].address.ObjectID })
	e.mu.Unlock()
	for _, r := range owned {
		if r.SQLIndex != nil {
			continue
		}
		if err = scope.acquireSequence(e.catalog.Domain.Ref(ref.Epoch, r.address), lock.AccessExclusive, false); err != nil {
			return Result{}, err
		}
	}
	// Dependency admission does not reserve replacement; release any existing
	// admission before validation, whose provider may replace the catalog.
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	e.mu.Lock()
	auth, err = e.statementAuthorizationLocked(op, s)
	if err != nil {
		return fail(err)
	}
	current, err := e.resolveRelationReference(auth, s.Name, true)
	if err != nil {
		return fail(err)
	}
	if current != ref || auth.catalog.Get(ref.Address) != old {
		return fail(ids.ErrCatalogReplaced)
	}
	if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	liveDestination, err := e.creationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Schema, {Text: old.name}}})
	if err != nil {
		return fail(err)
	}
	if liveDestination != destination {
		return fail(ids.ErrCatalogReplaced)
	}
	if err = auth.CheckCreate(destination); err != nil {
		return fail(err)
	}
	group := make([]*relationDescriptor, 0, len(owned)+1)
	group = append(group, old)
	for _, r := range owned {
		live, ok := auth.catalog.Relation(r.address)
		if ok && r.SQLIndex != nil {
			if live.SQLIndex != r.SQLIndex || live.namespace != old.namespace || live.Owner != old.Owner {
				return fail(ids.ErrCatalogReplaced)
			}
			group = append(group, live)
			continue
		}
		if !ok || live.RelationKind != sequenceRelation || live.Sequence.Owned != r.Sequence.Owned || live.namespace != old.namespace || live.Owner != old.Owner {
			return fail(ids.ErrCatalogReplaced)
		}
		group = append(group, live)
	}
	collision := func(r *relationDescriptor) error {
		return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists in schema "%s"`, r.name, s.Schema.Text))
	}
	typeCollision := func(r *relationDescriptor) error {
		return sqlError("42710", fmt.Sprintf(`type "%s" already exists in schema "%s"`, r.name, s.Schema.Text))
	}
	next := make([]relationDescriptor, len(group))
	for i, r := range group {
		if _, exists := auth.catalog.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: destination.Address, Name: r.name}); exists {
			return fail(collision(r))
		}
		if r.RowType != 0 {
			if winner, exists := auth.catalog.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: destination.Address, Name: r.name}); exists && winner != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.RowType)}) {
				return fail(typeCollision(r))
			}
		}
		frontier := descriptorRevisionFrontier(r)
		if frontier == ids.DefinitionRevision(^uint64(0)) {
			return fail(fmt.Errorf("stuntgres: definition revision exhausted"))
		}
		next[i] = *r
		next[i].namespace, next[i].revision = destination.Address, frontier+1
		if r.RelationKind == sequenceRelation {
			next[i].SequencePlanStamp = frontier + 1
		}
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for i := range next {
		if err = stage.Replace(&next[i]); err != nil {
			return Result{}, err
		}
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: ref.Epoch})
	}
	if _, conflict := err.(CatalogConflict); conflict {
		e.mu.Lock()
		current := e.stageCatalogBaseLocked(stage)
		if current.epoch == ref.Epoch {
			for _, r := range group {
				winner, exists := current.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: destination.Address, Name: r.name})
				if exists && winner != r.address {
					err = collision(r)
					break
				}
				if r.RowType != 0 {
					if winner, exists := current.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: destination.Address, Name: r.name}); exists && winner != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.RowType)}) {
						err = typeCollision(r)
						break
					}
				}
			}
		}
		e.mu.Unlock()
	}
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	return Result{}, nil
}
