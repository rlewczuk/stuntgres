package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func renameMissing(err error) bool {
	d, ok := err.(*sqlstate.Error)
	return ok && (d.Code == "42P01" || d.Code == "3F000")
}

// Engine.mu and the owner-authorized AX grant are held on entry. Every
// return releases Engine.mu; relabeling never copies or scans row storage.
func (e *Engine) renameRelationLocked(scope *StatementScope, op *Operation, auth AuthorizationView, name parser.ObjectName, column, destination string, ifExists, columnRename bool, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	ref, err := e.resolveRelationReference(auth, name, true)
	if err == nil && ref != scope.lockedRef {
		return fail(ErrCatalogReplaced)
	}
	if ifExists && renameMissing(err) {
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
		op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, name.Parts[len(name.Parts)-1].Text)})
		return Result{}, nil
	}
	if err != nil {
		return fail(err)
	}
	if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	old := scope.lockedDescriptor.(*relationDescriptor)
	if columnRename && old.relationKind == indexRelation {
		return fail(tableKind(old, "rename"))
	}
	slot := -1
	if columnRename {
		if err := inheritanceColumnGuard(old, column, "rename", parser.Position{}); err != nil {
			return fail(err)
		}
		if err := inheritanceAlterGuard(auth.catalog, old); err != nil {
			return fail(err)
		}
		if old.relationKind == sequenceRelation {
			return fail(&sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809",
				Message: fmt.Sprintf(`cannot rename columns of relation "%s"`, old.name),
				Detail:  "This operation is not supported for sequences.",
			})
		}
		if isSystemColumn(column) {
			return fail(sqlError("0A000", fmt.Sprintf(`cannot rename system column "%s"`, column)))
		}
		var exists bool
		slot, exists = old.index[column]
		if !exists {
			return fail(sqlError("42703", fmt.Sprintf(`column "%s" does not exist`, column)))
		}
		if isSystemColumn(destination) {
			return fail(sqlError("42701", fmt.Sprintf(`column name "%s" conflicts with a system column name`, destination)))
		}
		if _, exists = old.index[destination]; exists {
			return fail(sqlError("42701", fmt.Sprintf(`column "%s" of relation "%s" already exists`, destination, old.name)))
		}
	} else {
		if err = auth.CheckCreate(ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: old.namespace}); err != nil {
			return fail(err)
		}
		if _, exists := auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: old.namespace, Name: destination}]; exists {
			return fail(sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, destination)))
		}
	}
	if old.revision == DefinitionRevision(^uint64(0)) {
		return fail(fmt.Errorf("stuntgres: definition revision exhausted"))
	}
	next := *old
	next.revision++
	if columnRename {
		next.columns = append([]ColumnDescriptor(nil), old.columns...)
		next.columns[slot].Name = destination
		next.index = make(map[string]int, len(old.index))
		for key, value := range old.index {
			next.index[key] = value
		}
		delete(next.index, column)
		next.index[destination] = slot
	} else {
		next.name = destination
		if old.relationKind == sequenceRelation {
			// Relation rename invalidates analyzed sequence source without
			// making every descriptor revision a sequence-plan event.
			next.sequencePlanStamp, err = e.revisionLocked()
			if err != nil {
				return fail(err)
			}
		}
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
	if !columnRename && old.sqlIndex != nil && old.sqlIndex.constraint != nil {
		if err = stageBackingIndexRename(stage, &next); err != nil {
			e.mu.Unlock()
			e.DiscardCatalogStage(stage)
			return Result{}, err
		}
	}
	e.mu.Unlock()
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	defer e.DiscardCatalogStage(stage)
	if err = stage.Replace(&next); err == nil {
		var prepared *PreparedCatalogPublication
		prepared, err = e.ValidateCatalogStage(op.Context(), stage)
		if err == nil {
			_, err = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: ref.Epoch})
		}
	}
	if _, conflict := err.(CatalogConflict); conflict && !columnRename {
		e.mu.Lock()
		current := e.stageCatalogBaseLocked(stage)
		winner, exists := current.names[NameKey{ClassID: RelationClassID, Namespace: old.namespace, Name: destination}]
		sameEpoch := current.epoch == ref.Epoch
		e.mu.Unlock()
		if sameEpoch && exists && winner != ref.Address {
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, destination))
		}
	}
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	return Result{}, nil
}
