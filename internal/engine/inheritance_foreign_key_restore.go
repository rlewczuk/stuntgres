package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// This is not a general valid ALTER whitelist. Only a successful isolated
// import of both actual relations, with a strict ordinary inheritance edge,
// can issue a receipt. The incumbent pending binder remains the sole FK binder.
func (e *Engine) captureInheritedForeignKey(op *session.Operation, raw *parser.AlterTable, created map[ids.ObjectAddress]ids.ObjectRef) (table.RestoredForeignKeys, bool, error) {
	if handled, err := table.InheritedForeignKeyShape(raw); !handled || err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	foreign := raw.Actions[0].ForeignKey
	pending, err := e.captureRestoredForeignKeys(op, []parser.ForeignKeyConstraint{*foreign})
	if err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	auth := ddlAuthorityOf(pending.Auth)
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) {
		return table.RestoredForeignKeys{}, false, sqlError("0A000", table.RestoreInheritedForeignKeyBoundary)
	}
	sourceRef, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	targetRef, err := e.resolveRelationReference(auth, foreign.Target, false)
	if err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	if err := table.CaptureInheritedForeignKey(auth.View, auth, sourceRef, targetRef, created, *foreign, &pending); err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	return pending, true, nil
}

// Comments and final attribute-rank repairs do not alter these key/provider
// observations. No later key, owner, type or provider incarnation can reuse them.
func validateRestoredInheritedForeignKey(e *Engine, receipt *table.RestoredInheritedForeignKey, op *session.Operation, declaration table.RestoredForeignKeys, view catalog.Read) error {
	auth := ddlAuthorityOf(declaration.Auth)
	auth.Catalog = view
	auth.Recapture(view)
	if receipt.Source != declaration.Child || receipt.Source.Domain() != e.catalog.Domain || receipt.Target.Domain() != e.catalog.Domain || receipt.Source.Epoch != view.Epoch() || receipt.Target.Epoch != view.Epoch() || auth.RoleRecord != e.roles.published || !partitionConstraintReplayImport(e, op, auth) {
		return ids.ErrCatalogReplaced
	}
	if !receipt.Unchanged(view) {
		return ids.ErrCatalogReplaced
	}
	return auth.Check(access.Requirement{Object: receipt.Source, OwnerOnly: true})
}
