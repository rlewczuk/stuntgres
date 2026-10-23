package storage

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/ids"
)

// VacuumCandidate owns only fresh headers/end maps; payloads and old roots stay
// independently owned. Positions is present only for actual current compaction.
type VacuumCandidate struct {
	Versions                    []RowVersion
	Ends                        *DeletionRoot
	Positions                   []int
	Live, Dead, Removed, Frozen int64
	Horizon                     uint64
}

func vacuumEnded(row RowVersion, ends *DeletionRoot, frontier ids.CommitSeq) bool {
	if row.Born == 0 || row.Born > frontier || ends == nil {
		return false
	}
	end, ok := ends.Rows[row.Row]
	return ok && end.Version == row.Version && end.End != 0 && end.End <= frontier
}
func vacuumFreeze(row RowVersion, frontier ids.CommitSeq, cutoff uint64) bool {
	return row.Born != 0 && row.Born <= frontier && !row.Frozen && row.InsertFullXID >= 3 && row.InsertFullXID < cutoff
}
func InspectVacuum(ctx context.Context, rows []RowVersion, ends *DeletionRoot, frontier ids.CommitSeq, cutoff uint64, retire bool) (VacuumCandidate, error) {
	c := VacuumCandidate{Versions: rows, Ends: ends, Horizon: cutoff}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return VacuumCandidate{}, err
		}
		dead := vacuumEnded(row, ends, frontier)
		if dead && retire {
			c.Removed++
			continue
		}
		if dead {
			c.Dead++
		} else if row.Born != 0 && row.Born <= frontier {
			c.Live++
		}
		if vacuumFreeze(row, frontier, cutoff) {
			c.Frozen++
		} else if !row.Frozen {
			if row.InsertFullXID == 0 || row.Born == 0 {
				c.Horizon = 0
			} else if c.Horizon != 0 && row.InsertFullXID < c.Horizon {
				c.Horizon = row.InsertFullXID
			}
		}
	}
	return c, ctx.Err()
}
func MaterializeVacuum(ctx context.Context, rows []RowVersion, ends *DeletionRoot, frontier ids.CommitSeq, cutoff uint64, c VacuumCandidate) (VacuumCandidate, error) {
	if c.Removed == 0 && c.Frozen == 0 {
		return c, ctx.Err()
	}
	c.Versions = make([]RowVersion, 0, len(rows)-int(c.Removed))
	if c.Removed > 0 {
		c.Positions = make([]int, len(rows))
		if ends != nil {
			c.Ends = &DeletionRoot{Rows: make(map[ids.RowID]RowEnd, len(ends.Rows))}
			for id, end := range ends.Rows {
				c.Ends.Rows[id] = end
			}
		}
	}
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return VacuumCandidate{}, err
		}
		if c.Removed > 0 && vacuumEnded(row, ends, frontier) {
			c.Positions[i] = -1
			delete(c.Ends.Rows, row.Row)
			continue
		}
		if c.Positions != nil {
			c.Positions[i] = len(c.Versions)
		}
		if vacuumFreeze(row, frontier, cutoff) {
			row.Frozen = true
		}
		c.Versions = append(c.Versions, row)
	}
	return c, ctx.Err()
}

// RemapVacuumIndex never evaluates keys or predicates. Keys/bindings and index
// validity remain the original authority; only retained physical positions move.
func RemapVacuumIndex(ctx context.Context, root *IndexRoot, old []RowVersion, c VacuumCandidate, store *Relation, generation uint64) (*IndexRoot, error) {
	if root == nil {
		return nil, nil
	}
	if c.Positions == nil {
		if root.Hash == nil || SameVersionRoot(c.Versions, old) {
			return root, ctx.Err()
		}
		next, h := *root, *root.Hash
		h.Storage, h.Generation, h.Ends = store, generation, c.Ends
		next.Hash = &h
		return &next, ctx.Err()
	}
	position := func(row ids.RowID, version ids.VersionID, p int) (int, error) {
		if p < 0 || p >= len(old) || old[p].Row != row || old[p].Version != version {
			return 0, fmt.Errorf("stuntgres: invalid vacuum index position")
		}
		return c.Positions[p], nil
	}
	next := &IndexRoot{}
	if root.Entries != nil {
		next.Entries = make([]IndexEntry, 0, len(root.Entries))
		for _, entry := range root.Entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			p, err := position(entry.Row, entry.Version, entry.Position)
			if err != nil {
				return nil, err
			}
			if p < 0 {
				continue
			}
			entry.Position = p
			next.Entries = append(next.Entries, entry)
		}
	}
	if root.Hash != nil {
		h := *root.Hash
		h.Buckets = make(map[uint64][]HashEntry, len(root.Hash.Buckets))
		if h.Prefix < 0 || h.Prefix > len(old) {
			return nil, fmt.Errorf("stuntgres: invalid vacuum hash prefix")
		}
		prefix := 0
		for _, p := range c.Positions[:h.Prefix] {
			if p >= 0 {
				prefix++
			}
		}
		h.Storage, h.Generation, h.Prefix, h.Ends = store, generation, prefix, c.Ends
		if root.Hash.RowEntries != nil {
			h.RowEntries = make(map[ids.RowID]*HashEntry, len(root.Hash.RowEntries))
		}
		for key, bucket := range root.Hash.Buckets {
			entries := make([]HashEntry, 0, len(bucket))
			for _, entry := range bucket {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				p, err := position(entry.Row, entry.Version, entry.Position)
				if err != nil {
					return nil, err
				}
				if p < 0 {
					continue
				}
				entry.Position = p
				entries = append(entries, entry)
			}
			if len(entries) > 0 {
				h.Buckets[key] = entries
				if h.RowEntries != nil {
					for i := range entries {
						h.RowEntries[entries[i].Row] = &entries[i]
					}
				}
			}
		}
		next.Hash = &h
	}
	if root.Membership != nil {
		next.Membership = make(map[ids.RowID]ids.VersionID, len(root.Membership))
		for _, row := range c.Versions {
			if root.Membership[row.Row] == row.Version {
				next.Membership[row.Row] = row.Version
			}
		}
	}
	if root.RowEntries != nil {
		next.RowEntries = make(map[ids.RowID]int, len(root.RowEntries))
		for i, entry := range next.Entries {
			if _, ok := root.RowEntries[entry.Row]; ok {
				next.RowEntries[entry.Row] = i
			}
		}
	}
	return next, ctx.Err()
}
