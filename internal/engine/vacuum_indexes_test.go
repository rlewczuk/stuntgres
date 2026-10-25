package engine

import (
	"context"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// vacuumScan is one VACUUM heap pass over rows: inspection, then
// materialization when it removes or freezes a version.
func vacuumScan(ctx context.Context, rows []storage.RowVersion, ends *storage.DeletionRoot, frontier ids.CommitSeq, cutoff uint64, retire bool) (storage.VacuumCandidate, error) {
	c, err := storage.InspectVacuum(ctx, rows, ends, frontier, cutoff, retire)
	if err != nil {
		return c, err
	}
	return storage.MaterializeVacuum(ctx, rows, ends, frontier, cutoff, c)
}
