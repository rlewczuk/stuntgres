package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Engine.mu and the owner-authorized AX grant are held on entry. Every
// return releases Engine.mu; relabeling never copies or scans row storage.
func (e *Engine) renameRelationLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, name parser.ObjectName, column, destination string, ifExists, columnRename, allowIndexColumn, only bool, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	ref, err := e.resolveRelationReference(auth, name, true)
	if err == nil && ref != scope.lockedRef {
		return fail(ids.ErrCatalogReplaced)
	}
	if ifExists && table.RenameMissing(err) {
		return e.skipMissingRelationLocked(scope, op, name.Parts[len(name.Parts)-1].Text, false)
	}
	if err != nil {
		return fail(err)
	}
	if err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	old := scope.lockedDescriptor.(*catalog.Relation)
	if columnRename && old.RelationKind == catalog.IndexRelation && !allowIndexColumn {
		return fail(analyze.TableKindError(old, "rename"))
	}
	var renamed *catalog.Relation
	if columnRename {
		if old.OfType != (ids.ObjectAddress{}) {
			return fail(sqlError("42809", "cannot rename column of typed table"))
		}
		if err := table.InheritanceColumnGuard(old, column, "rename", parser.Position{}); err != nil {
			return fail(err)
		}
		if catalog.InheritanceParticipant(auth.Catalog, old) {
			return e.renameInheritanceColumnLocked(scope, op, auth, old, column, destination, only)
		}
		if old.RelationKind == catalog.SequenceRelation {
			return fail(&sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809",
				Message: fmt.Sprintf(`cannot rename columns of relation "%s"`, old.Name()),
				Detail:  "This operation is not supported for sequences.",
			})
		}
		renamed, err = table.RenameColumnCandidate(old, column, destination)
		if err != nil {
			return fail(err)
		}
	} else {
		if err = auth.CheckCreate(e.catalog.Domain.Ref(ref.Epoch, old.Namespace())); err != nil {
			return fail(err)
		}
		if _, exists := auth.Catalog.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: old.Namespace(), Name: destination}); exists {
			return fail(sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, destination)))
		}
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return fail(fmt.Errorf("stuntgres: definition revision exhausted"))
	}
	next := *old
	next.ObjectRevision++
	if columnRename {
		next.Columns, next.Index = renamed.Columns, renamed.Index
	} else {
		next.ObjectName = destination
		if old.RelationKind == catalog.SequenceRelation {
			// Relation rename invalidates analyzed sequence source without
			// making every descriptor revision a sequence-plan event.
			next.SequencePlanStamp, err = e.catalog.revisionLocked()
			if err != nil {
				return fail(err)
			}
		}
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	if !columnRename && old.SQLIndex != nil && old.SQLIndex.Constraint != nil {
		if err = index.StageBackingIndexRename(stage, &next); err != nil {
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
			_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: ref.Epoch})
		}
	}
	if _, conflict := err.(catalog.Conflict); conflict && !columnRename {
		e.mu.Lock()
		current := e.stageCatalogBaseLocked(stage)
		winner, exists := current.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: old.Namespace(), Name: destination})
		sameEpoch := current.Epoch() == ref.Epoch
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
