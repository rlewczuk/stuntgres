package core

import (
	"context"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/storage"
)

// Run only inside the isolated admitted repair statement, after scalar/provider
// remint and before index transport. Published target/TEMP roots are never touched.
func (stage *Engine) repairRemappedHashPartitions(ctx context.Context, evaluation *exec.Context, auth AuthorizationView, snapshot *index.Snapshot) (map[ids.ObjectAddress]bool, error) {
	repair, err := partition.RepairRemappedHash(ctx, evaluation, auth.View, auth, stage.Storage.Relations, snapshot)
	if err != nil {
		return nil, err
	}
	for address := range repair.Dirty {
		original := stage.Storage.Relations[address]
		if original == nil {
			return nil, ids.ErrCatalogReplaced
		}
		next := *original
		next.Versions = make([]storage.RowVersion, 0, len(original.Versions)-len(repair.Removed[address])+len(repair.Incoming[address]))
		for i, version := range original.Versions {
			if !repair.Removed[address][i] {
				next.Versions = append(next.Versions, version)
			}
		}
		next.Versions = append(next.Versions, repair.Incoming[address]...)
		next.Generation++
		stage.Mu.Lock()
		stage.Storage.InstallRelationLocked(address, &next)
		stage.Mu.Unlock()
	}
	return repair.Dirty, ctx.Err()
}
