package engine

import (
	"context"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func decodeRestoreExtendedStatistics(r *restoreRun, comment string, _ parser.Position) (bool, error) {
	record, recognized, err := storage.DecodeExtendedStatisticsSnapshot(comment)
	if err != nil || !recognized {
		return recognized, err
	}
	r.extendedStatistics = append(r.extendedStatistics, record)
	return true, nil
}

func finishRestoreExtendedStatistics(r *restoreRun, ctx context.Context, op *session.Operation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.stage.Mu.Lock()
	defer r.stage.Mu.Unlock()
	if _, err := r.stage.AuthorizationLocked(op); err != nil {
		return err
	}
	read := r.stage.Catalog.Current()
	metadata, err := storage.RestoreExtendedStatisticsSnapshots(ctx, read, r.extendedStatistics, core.StatisticsSnapshotCodec(ctx, r.stage, read))
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
