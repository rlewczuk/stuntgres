package engine

import (
	"context"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func decodeRestoreNDistinct(r *restoreRun, comment string, _ parser.Position) (bool, error) {
	record, recognized, err := storage.DecodeNDistinctSnapshot(comment)
	if err != nil || !recognized {
		return recognized, err
	}
	r.ndistinct = append(r.ndistinct, record)
	return true, nil
}

func finishRestoreNDistinct(r *restoreRun, ctx context.Context, op *session.Operation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.stage.Mu.Lock()
	defer r.stage.Mu.Unlock()
	if _, err := r.stage.AuthorizationLocked(op); err != nil {
		return err
	}
	metadata, err := storage.RestoreNDistinctSnapshots(r.stage.Catalog.Current(), r.ndistinct)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// The isolated engine is not externally visible. Destination publication
	// reconstructs these checked facts in its own generation before installation.
	r.stage.Storage.RelationMetadata = metadata
	return nil
}
