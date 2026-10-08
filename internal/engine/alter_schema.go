package engine

import (
	"fmt"
	"sort"
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
		return fail(ErrCatalogReplaced)
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
	if old.relationKind != tableRelation && old.relationKind != viewRelation {
		return fail(sqlError("0A000", "ALTER TABLE SET SCHEMA supports ordinary tables only"))
	}
	destination, err := e.creationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Schema, {Text: old.name}}})
	if err != nil {
		return fail(err)
	}
	if err = checkTableCreationNamespace(auth.catalog, destination.Address); err != nil {
		return fail(err)
	}
	if err = auth.CheckCreate(destination); err != nil {
		return fail(err)
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
	for _, d := range auth.catalog.state.descriptors {
		r, ok := d.(*relationDescriptor)
		if ok && r.sqlIndex != nil && r.sqlIndex.parent == old.address {
			owned = append(owned, r)
			continue
		}
		if !ok || r.relationKind != sequenceRelation {
			continue
		}
		a := r.sequence.owned
		if old.relationKind == tableRelation && a.ClassID == old.address.ClassID && a.ObjectID == old.address.ObjectID && old.hasAttribute(a.SubID) {
			owned = append(owned, r)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].address.ObjectID < owned[j].address.ObjectID })
	e.mu.Unlock()
	for _, r := range owned {
		if r.sqlIndex != nil {
			continue
		}
		if err = scope.acquireSequence(ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: r.address}, accessExclusive, false); err != nil {
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
	if current != ref || auth.catalog.state.descriptors[ref.Address] != old {
		return fail(ErrCatalogReplaced)
	}
	if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	liveDestination, err := e.creationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Schema, {Text: old.name}}})
	if err != nil {
		return fail(err)
	}
	if liveDestination != destination {
		return fail(ErrCatalogReplaced)
	}
	if err = auth.CheckCreate(destination); err != nil {
		return fail(err)
	}
	group := make([]*relationDescriptor, 0, len(owned)+1)
	group = append(group, old)
	for _, r := range owned {
		live, ok := auth.catalog.state.descriptors[r.address].(*relationDescriptor)
		if ok && r.sqlIndex != nil {
			if live.sqlIndex != r.sqlIndex || live.namespace != old.namespace || live.owner != old.owner {
				return fail(ErrCatalogReplaced)
			}
			group = append(group, live)
			continue
		}
		if !ok || live.relationKind != sequenceRelation || live.sequence.owned != r.sequence.owned || live.namespace != old.namespace || live.owner != old.owner {
			return fail(ErrCatalogReplaced)
		}
		group = append(group, live)
	}
	collision := func(r *relationDescriptor) error {
		return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists in schema "%s"`, r.name, s.Schema.Text))
	}
	next := make([]relationDescriptor, len(group))
	for i, r := range group {
		if _, exists := auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: destination.Address, Name: r.name}]; exists {
			return fail(collision(r))
		}
		frontier := descriptorRevisionFrontier(r)
		if frontier == DefinitionRevision(^uint64(0)) {
			return fail(fmt.Errorf("stuntgres: definition revision exhausted"))
		}
		next[i] = *r
		next[i].namespace, next[i].revision = destination.Address, frontier+1
		if r.relationKind == sequenceRelation {
			next[i].sequencePlanStamp = frontier + 1
		}
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for i := range next {
		if err = stage.Replace(&next[i]); err != nil {
			return Result{}, err
		}
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: ref.Epoch})
	}
	if _, conflict := err.(CatalogConflict); conflict {
		e.mu.Lock()
		if e.state.epoch == ref.Epoch {
			for _, r := range group {
				winner, exists := e.state.names[NameKey{ClassID: RelationClassID, Namespace: destination.Address, Name: r.name}]
				if exists && winner != r.address {
					err = collision(r)
					break
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
