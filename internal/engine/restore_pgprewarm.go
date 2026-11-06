package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func decodeRestoreAuxiliaryForks(r *restoreRun, comment string, _ parser.Position) (bool, error) {
	record, recognized, err := storage.DecodeAuxiliaryForkSnapshot(comment)
	if err != nil || !recognized { return recognized, err }
	r.auxiliaryForks = append(r.auxiliaryForks, record)
	return true, nil
}
func finishRestoreAuxiliaryForks(r *restoreRun, ctx context.Context, op *session.Operation) error {
	if err := ctx.Err(); err != nil { return err }
	if len(r.auxiliaryForks) == 0 { return nil }
	r.stage.Mu.Lock()
	defer r.stage.Mu.Unlock()
	if _, err := r.stage.AuthorizationLocked(op); err != nil { return err }
	records, err := storage.RestoreAuxiliaryForkSnapshots(ctx, r.stage.Catalog.Current(), r.auxiliaryForks)
	if err != nil { return err }
	for address := range records { if r.stage.Storage.Relations[address] == nil { return ids.ErrCatalogReplaced } }
	if err := ctx.Err(); err != nil { return err }
	// Only the private stage is changed. Replacement remints real destination
	// storage; neither this record nor native Dump transports cache occupancy.
	for address, forks := range records { r.stage.Storage.Relations[address].Auxiliary = forks }
	return nil
}
