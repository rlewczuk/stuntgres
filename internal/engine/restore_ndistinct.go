package engine

import (
	"context"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func decodeRestoreExtendedStatistics(r *restoreRun, comment string, _ parser.Position) (bool, error) {
	relation, recognized, err := storage.DecodeRelationStatisticsSnapshot(comment)
	if err != nil { return recognized, err }
	if recognized { r.relationStatistics = append(r.relationStatistics, relation); return true, nil }
	attribute, recognized, err := storage.DecodeAttributeStatisticsSnapshot(comment)
	if err != nil { return recognized, err }
	if recognized { r.attributeStatistics = append(r.attributeStatistics, attribute); return true, nil }
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
	attributes, err := storage.RestoreAttributeStatisticsSnapshots(ctx, read, r.attributeStatistics, core.StatisticsSnapshotCodec(ctx, r.stage, read))
	if err != nil { return err }
	if metadata == nil { metadata = attributes } else {
		for address, entry := range attributes {
			if previous := metadata[address]; previous != nil { entry.Statistics = storage.MergeExtendedStatistics(entry.Statistics, previous.Statistics.Extended) }
			metadata[address] = entry
		}
	}
	relations, err := storage.RestoreRelationStatisticsSnapshots(ctx, read, r.relationStatistics)
	if err != nil { return err }
	metadata = storage.MergeRelationStatisticsMetadata(metadata, relations)
	if err := ctx.Err(); err != nil {
		return err
	}
	// The isolated engine is not externally visible. Destination publication
	// reconstructs these checked facts in its own generation before installation.
	r.stage.Storage.RelationMetadata = metadata
	return nil
}
