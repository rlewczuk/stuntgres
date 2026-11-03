package storage

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/ids"
)

// RewriteVacuum owns a fresh heap/header/end root even for a clean or empty
// predecessor. Immutable datum backing and logical insertion identity survive.
// The canonical retirement guard, not AX alone, authorizes removing dead heads
// and the captured genuine UPDATE predecessors.
func RewriteVacuum(ctx context.Context, rows []RowVersion, ends *DeletionRoot, retained *HeapPredecessor, frontier ids.CommitSeq, cutoff uint64, retire bool, progress VacuumProgress) (VacuumCandidate, error) {
	c, err := InspectVacuum(ctx, rows, ends, frontier, cutoff, retire, VacuumProgress{Fetched: progress.Fetched})
	if err != nil {
		return VacuumCandidate{}, err
	}
	c.Versions = make([]RowVersion, 0, len(rows)-int(c.Removed))
	c.Positions = make([]int, len(rows))
	c.Ends = &DeletionRoot{}
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return VacuumCandidate{}, err
		}
		if retire && vacuumEnded(row, ends, frontier) {
			c.Positions[i] = -1
		} else {
			c.Positions[i] = len(c.Versions)
			if vacuumFreeze(row, frontier, cutoff) {
				row.Frozen = true
			}
			c.Versions = append(c.Versions, row)
			if ends != nil {
				if end, ok := ends.Lookup(row.Row, row.Version); ok {
					c.Ends.Set(row.Row, end)
				}
			}
		}
		if progress.Completed != nil && ((i+1)%128 == 0 || i+1 == len(rows)) {
			progress.Completed(i+1, int64(len(c.Versions)))
		}
	}
	var dead, removed int64
	c.Retained, dead, removed, err = InspectRetainedVacuum(ctx, retained, frontier, retire)
	if err == nil {
		c.Dead, err = AddSize(c.Dead, dead)
	}
	if err == nil {
		c.Removed, err = AddSize(c.Removed, removed)
	}
	if err != nil {
		return VacuumCandidate{}, err
	}
	return c, ctx.Err()
}

// NewRewriteRelation prepares the new incarnation without publishing anything.
func NewRewriteRelation(c VacuumCandidate, generation uint64, maintenance Maintenance) (*Relation, error) {
	if generation == ^uint64(0) {
		return nil, fmt.Errorf("stuntgres: storage generation exhausted")
	}
	if _, err := AddSize(c.Live, c.Dead); err != nil {
		return nil, err
	}
	maintenance.RecordRewrite(c)
	return &Relation{Versions: c.Versions, Ends: c.Ends, Retained: c.Retained, Discarded: c.Discarded, Headers: c.Headers, Generation: generation + 1, Maintenance: maintenance}, nil
}
