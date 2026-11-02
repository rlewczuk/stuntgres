package storage

import (
	"context"

	"stuntgres/internal/engine/ids"
)

// RewriteHeapVacuum preserves the incumbent rewrite identity contract while
// carrying every visible repaired version into the new incarnation. Old captured
// roots are never modified. Physical history is not exported to native controls.
func RewriteHeapVacuum(ctx context.Context, source Relation, frontier ids.CommitSeq, cutoff uint64, retire bool, progress func(int, int64)) (VacuumCandidate, error) {
	rows := EffectiveHeapRows(source.Versions, source.Headers)
	c, err := RewriteVacuum(ctx, rows, source.Ends, nil, frontier, cutoff, retire, progress)
	if err != nil {
		return VacuumCandidate{}, err
	}
	c, err = MaintainHeapHistory(ctx, c, source, frontier, retire, false, false)
	if err != nil {
		return VacuumCandidate{}, err
	}
	if source.Headers != nil && len(source.Headers.Additional) != 0 {
		present := make(map[ids.VersionID]bool, len(c.Versions))
		for _, row := range c.Versions {
			present[row.Version] = true
		}
		for _, original := range source.Headers.Additional {
			if err := ctx.Err(); err != nil {
				return VacuumCandidate{}, err
			}
			if present[original.Version] {
				continue
			}
			row := source.Headers.Apply(original)
			if row.ForcedFrozen && (row.ForcedEnd == 0 || row.ForcedEnd > frontier) {
				c.Versions = append(c.Versions, row)
				present[row.Version] = true
			}
		}
	}
	return c, ctx.Err()
}
