package index

import (
	"context"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// OwnExclusionEntry finds candidate's own published entry on idx in the scan.
func OwnExclusionEntry(scan *ExclusionScan, ticket *lock.Ticket, idx ids.ObjectAddress, candidate storage.RowVersion) (ExclusionEntry, bool) {
	if own := scan.Pending[ticket]; own != nil {
		for _, entry := range own.Entries[idx] {
			if entry.Row.Row == candidate.Row && entry.Row.Version == candidate.Version {
				return entry, true
			}
		}
	}
	return ExclusionEntry{}, false
}

// ExclusionVisit is the outcome of one pass over a captured exclusion scan.
// Potential reports any overlapping entry; Wait names an owner to wait for
// before the pass is retaken; Hit reports the first conflict the statement
// cannot wait out, with its keys, the existing row and whether it is
// committed.
type ExclusionVisit struct {
	Potential     bool
	Wait          *lock.Ticket
	Hit           bool
	Key, Existing []sqltype.Value
	Row           storage.RowVersion
	Committed     bool
}

// VisitExclusion compares current, the candidate's own entry, against the
// committed root and every owner's pending projection in scan. It asks op for
// its context at each key comparison, as that context can change while COPY
// input is held.
func VisitExclusion(op interface{ Context() context.Context }, evaluation *exec.Context, scan *ExclusionScan, ticket *lock.Ticket, parent ids.ObjectAddress, bound *UniqueBoundKey, current ExclusionEntry, purpose ProbePurpose) (ExclusionVisit, error) {
	idx := bound.Index
	own := scan.Pending[ticket]
	var v ExclusionVisit
	visit := func(entry ExclusionEntry, owner *lock.Ticket, committed bool) error {
		if owner == ticket && entry.Row.Row == current.Row.Row {
			return nil
		}
		if owner != nil && owner != ticket && !entry.Admitted {
			return nil
		}
		if own != nil && own.Ended[parent][entry.Row.Row] == entry.Row.Version && committed {
			return nil
		}
		match, err := constraintKeysMatch(op.Context(), bound, current.Keys, entry.Keys)
		if err != nil || !match {
			return err
		}
		if purpose != ProbeRevalidation && idx.SQLIndex.HasRuntimeDomainKeyConversion() {
			if err := bound.Ordering.FillKeys(entry.Row.Values, bound.Scratch, evaluation); err != nil {
				return err
			}
			match, err = constraintKeysMatch(op.Context(), bound, current.Keys, bound.Scratch)
			if err != nil || !match {
				return err
			}
		}
		if committed {
			for pending, p := range scan.Pending {
				if pending != ticket && p.Ended[parent][entry.Row.Row] == entry.Row.Version {
					v.Potential = true
					v.Wait = pending
					return nil
				}
			}
		}
		v.Potential = true
		if owner != nil && owner != ticket {
			v.Wait = owner
			return nil
		}
		if !v.Hit {
			existing := entry.Keys
			if purpose != ProbeRevalidation && idx.SQLIndex.HasRuntimeDomainKeyConversion() {
				existing = slices.Clone(bound.Scratch)
			} else if committed && scan.Root.Hash != nil {
				existing = slices.Clone(existing)
			}
			v.Hit, v.Key, v.Existing, v.Row, v.Committed = true, current.Keys, existing, entry.Row, committed
		}
		return nil
	}
	if scan.Root != nil {
		if scan.Root.Hash != nil {
			var keys [1]sqltype.Value
			for _, bucket := range scan.Root.Hash.Buckets {
				for _, entry := range bucket {
					row, err := ConflictEntryVersion(scan.Versions, storage.IndexEntry{Row: entry.Row, Version: entry.Version, Position: entry.Position})
					if err != nil {
						return ExclusionVisit{}, err
					}
					keys[0] = entry.Key
					if err := visit(ExclusionEntry{Row: row, Keys: keys[:]}, nil, true); err != nil {
						return ExclusionVisit{}, err
					}
				}
			}
		} else {
			start := 0
			equality := !idx.SQLIndex.IsExclusion()
			if equality {
				var err error
				start, err = UniqueRootStart(op.Context(), &bound.Ordering, scan.Root.Entries, current.Keys)
				if err != nil {
					return ExclusionVisit{}, err
				}
			}
			for at := start; at < len(scan.Root.Entries); at++ {
				entry := scan.Root.Entries[at]
				if equality {
					order, err := bound.Ordering.CompareKeys(op.Context(), entry, storage.IndexEntry{Keys: current.Keys})
					if err != nil {
						return ExclusionVisit{}, err
					}
					if order != 0 {
						break
					}
				}
				row, err := ConflictEntryVersion(scan.Versions, entry)
				if err != nil {
					return ExclusionVisit{}, err
				}
				if err := visit(ExclusionEntry{Row: row, Keys: entry.Keys}, nil, true); err != nil {
					return ExclusionVisit{}, err
				}
			}
		}
	}
	for owner, p := range scan.Pending {
		for _, entry := range p.Entries[idx.Address()] {
			if err := visit(entry, owner, false); err != nil {
				return ExclusionVisit{}, err
			}
		}
	}
	return v, nil
}

// ExclusionDeferred also covers equality-backed constraint indexes. Admission
// records potential conflicts; only a due-boundary recheck waits or rejects.
func ExclusionDeferred(parent, idx *catalog.Relation, recheck bool) (bool, error) {
	_, deferrable, _, found := IndexConstraintTiming(parent, idx)
	if !found {
		return false, ids.ErrCatalogReplaced
	}
	return deferrable && !recheck, nil
}

func constraintKeysMatch(ctx context.Context, bound *UniqueBoundKey, left, right []sqltype.Value) (bool, error) {
	if bound.Index.SQLIndex.IsExclusion() {
		return ExclusionKeysMatch(ctx, bound.Index.SQLIndex, left, right)
	}
	if !bound.Index.SQLIndex.NullsNotDistinct && (UniqueHasNull(left) || UniqueHasNull(right)) {
		return false, nil
	}
	comparison, err := bound.Ordering.CompareKeys(ctx, storage.IndexEntry{Keys: left}, storage.IndexEntry{Keys: right})
	return comparison == 0, err
}
