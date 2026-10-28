package engine

import (
	"context"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/partitioncmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func decodeRestorePendingDetach(r *restoreRun, comment string, _ parser.Position) (bool, error) {
	record, recognized, err := partition.DecodePendingDetach(comment)
	if err != nil || !recognized {
		return recognized, err
	}
	r.pendingDetaches = append(r.pendingDetaches, record)
	return true, nil
}

func finishRestorePendingDetaches(r *restoreRun, ctx context.Context, op *session.Operation) error {
	return partitioncmds.ImportPendingDetaches(r.stage, ctx, op, r.pendingDetaches)
}
