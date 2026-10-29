package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func decodeRestoreInitialPrivileges(r *restoreRun, comment string, _ parser.Position) (bool, error) {
	metadata, recognized, err := control.DecodeInitialPrivileges(comment)
	if err != nil || !recognized {
		return recognized, err
	}
	if r.initialPrivileges != nil {
		return true, fmt.Errorf("stuntgres: duplicate initial privileges metadata")
	}
	r.initialPrivileges = metadata
	return true, nil
}

func finishRestoreInitialPrivileges(r *restoreRun, ctx context.Context, op *session.Operation) error {
	if r.initialPrivileges == nil {
		return ctx.Err()
	}
	e := r.stage
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	records, err := control.ResolveInitialPrivileges(ctx, auth.Catalog, auth.Roles, r.initialPrivileges)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	epoch := auth.Catalog.Epoch()
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	// Remove first, including replay-generated bootstrap/extension records.
	// This also prevents capture's origin from inheriting the replay origin.
	for address := range stage.Base.State().InitialPrivileges() {
		stage.RemoveInitialPrivileges(address)
	}
	for address, record := range records {
		stage.RecordInitialPrivilege(address, record.PrivType, record.ACL)
	}
	if err := catalog.ValidateInitialPrivileges(auth.Roles, e.Catalog.Read(stage.State())); err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		return err
	}
	_, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: epoch})
	return err
}
