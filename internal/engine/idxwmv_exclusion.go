package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// ContextSource yields the operation's current context. The exclusion visit
// asks it once per key comparison, as the operation's context can change
// while COPY input is held.
type ContextSource interface {
	Context() context.Context
}

// OwnExclusionEntry finds candidate's own published entry on idx in the scan.
func OwnExclusionEntry(scan *index.ExclusionScan, ticket *lock.Ticket, idx ids.ObjectAddress, candidate storage.RowVersion) (index.ExclusionEntry, bool) {
	if own := scan.Pending[ticket]; own != nil {
		for _, entry := range own.Entries[idx] {
			if entry.Row.Row == candidate.Row && entry.Row.Version == candidate.Version {
				return entry, true
			}
		}
	}
	return index.ExclusionEntry{}, false
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
// committed root and every owner's pending projection in scan.
func VisitExclusion(op ContextSource, evaluation *exec.Context, scan *index.ExclusionScan, ticket *lock.Ticket, parent ids.ObjectAddress, bound *index.UniqueBoundKey, current index.ExclusionEntry, purpose index.ProbePurpose) (ExclusionVisit, error) {
	idx := bound.Index
	own := scan.Pending[ticket]
	var v ExclusionVisit
	visit := func(entry index.ExclusionEntry, owner *lock.Ticket, committed bool) error {
		if owner == ticket && entry.Row.Row == current.Row.Row {
			return nil
		}
		if owner != nil && owner != ticket && !entry.Admitted {
			return nil
		}
		if own != nil && own.Ended[parent][entry.Row.Row] == entry.Row.Version && committed {
			return nil
		}
		match, err := index.ExclusionKeysMatch(op.Context(), idx.SQLIndex, current.Keys, entry.Keys)
		if err != nil || !match {
			return err
		}
		if purpose != index.ProbeRevalidation && idx.SQLIndex.HasRuntimeDomainKeyConversion() {
			if err := bound.Ordering.FillKeys(entry.Row.Values, bound.Scratch, evaluation); err != nil {
				return err
			}
			match, err = index.ExclusionKeysMatch(op.Context(), idx.SQLIndex, current.Keys, bound.Scratch)
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
			if purpose != index.ProbeRevalidation && idx.SQLIndex.HasRuntimeDomainKeyConversion() {
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
					row, err := index.ConflictEntryVersion(scan.Versions, storage.IndexEntry{Row: entry.Row, Version: entry.Version, Position: entry.Position})
					if err != nil {
						return ExclusionVisit{}, err
					}
					keys[0] = entry.Key
					if err := visit(index.ExclusionEntry{Row: row, Keys: keys[:]}, nil, true); err != nil {
						return ExclusionVisit{}, err
					}
				}
			}
		} else {
			for _, entry := range scan.Root.Entries {
				row, err := index.ConflictEntryVersion(scan.Versions, entry)
				if err != nil {
					return ExclusionVisit{}, err
				}
				if err := visit(index.ExclusionEntry{Row: row, Keys: entry.Keys}, nil, true); err != nil {
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

// ExclusionDeferred reports whether idx's constraint on parent is deferrable
// and this is not the recheck, so a potential conflict is only recorded.
func ExclusionDeferred(parent, idx *catalog.Relation, recheck bool) (bool, error) {
	c := index.ExclusionDefinition(parent, idx)
	if c == nil {
		return false, ids.ErrCatalogReplaced
	}
	return c.Deferrable && !recheck, nil
}

// ExclusionShapeEntries projects the statement's surviving writes on parent
// into the admitted entries of every exclusion index of the new shape.
func ExclusionShapeEntries(evaluation *exec.Context, parent *catalog.Relation, view catalog.Read, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) (map[ids.ObjectAddress][]index.ExclusionEntry, error) {
	entries := make(map[ids.ObjectAddress][]index.ExclusionEntry, len(parent.Constraints.Exclusion))
	for _, c := range parent.Constraints.Exclusion {
		idx, ok := view.Relation(c.Index)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing private exclusion index")
		}
		ordering, err := index.BindExclusionOrdering(parent, idx.SQLIndex)
		if err != nil {
			return nil, err
		}
		var preparation index.ExecutionPreparation
		for _, row := range writes {
			if index.UniqueRowDeleted(row.Row, deletes) {
				continue
			}
			var keys []sqltype.Value
			if idx.SQLIndex.RequiresInsertionOutput() {
				output, ok := row.IndexOutputs[idx.Address()]
				if !ok || !index.OutputMatches(output, idx) {
					return nil, fmt.Errorf("stuntgres: exclusion shape lost produced index output")
				}
				if !output.Member {
					continue
				}
				if idx.SQLIndex.Method == catalog.HashMethod {
					if output.Key.IsNull() {
						continue
					}
					keys = []sqltype.Value{output.Key}
				} else {
					keys = output.Keys
				}
			} else {
				matches, err := preparation.PredicateMatches(evaluation, parent, idx.SQLIndex, row.Values, &ordering, nil)
				if err != nil {
					return nil, err
				}
				if !matches {
					continue
				}
				if err := preparation.PrepareKeys(evaluation, parent, idx.SQLIndex, &ordering, nil); err != nil {
					return nil, err
				}
				keys = make([]sqltype.Value, len(idx.SQLIndex.Keys))
				if err := ordering.FillKeys(row.Values, keys, evaluation); err != nil {
					return nil, err
				}
			}
			if index.UniqueHasNull(keys) {
				continue
			}
			entries[idx.Address()] = append(entries[idx.Address()], index.ExclusionEntry{Row: row, Keys: keys, Admitted: true})
		}
	}
	return entries, nil
}

// ProjectionReplaceShape replaces the projected entries of original's
// exclusion indexes by entries of the new shape and ends the rows the
// statement replaced or deleted.
func ProjectionReplaceShape(p *index.ExclusionProjection, original, parent *catalog.Relation, entries map[ids.ObjectAddress][]index.ExclusionEntry, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) {
	for _, c := range original.Constraints.Exclusion {
		delete(p.Entries, c.Index)
	}
	for idx, rows := range entries {
		p.Entries[idx] = rows
	}
	ends := maps.Clone(p.Ended[parent.Address()])
	if ends == nil {
		ends = make(map[ids.RowID]ids.VersionID)
	}
	for _, row := range writes {
		if row.Base != 0 {
			ends[row.Row] = row.Base
		}
	}
	if deletes != nil {
		for id, candidate := range deletes.Rows {
			ends[id] = candidate.Version
		}
	}
	p.Ended[parent.Address()] = ends
}

// ProjectionEndRow ends row at version on parent and drops its entries from
// every exclusion index of constraints.
func ProjectionEndRow(p *index.ExclusionProjection, parent ids.ObjectAddress, constraints []catalog.ExclusionConstraint, row ids.RowID, version ids.VersionID) {
	ends := maps.Clone(p.Ended[parent])
	if ends == nil {
		ends = make(map[ids.RowID]ids.VersionID)
	}
	ends[row] = version
	p.Ended[parent] = ends
	for _, c := range constraints {
		entries := p.Entries[c.Index]
		next := make([]index.ExclusionEntry, 0, len(entries))
		for _, entry := range entries {
			if entry.Row.Row != row {
				next = append(next, entry)
			}
		}
		p.Entries[c.Index] = next
	}
}

// ExclusionEditApply replaces row's entry on e.Index by its new one (none when
// it is not a member) and ends the rows e excludes. Entries of other indexes
// stay, so a later index insertion keeps the earlier projections.
func ExclusionEditApply(e index.ExclusionEdit, p *index.ExclusionProjection, parent ids.ObjectAddress, row storage.RowVersion) {
	entries := p.Entries[e.Index]
	next := make([]index.ExclusionEntry, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.Row.Row != row.Row {
			next = append(next, entry)
		}
	}
	if e.Member {
		next = append(next, index.ExclusionEntry{Row: row, Keys: e.Keys, Admitted: e.Admitted})
	}
	p.Entries[e.Index] = next
	if len(e.Excluded) != 0 {
		ends := maps.Clone(p.Ended[parent])
		if ends == nil {
			ends = make(map[ids.RowID]ids.VersionID)
		}
		for _, old := range e.Excluded {
			ends[old.Row] = old.Version
			if old.Base != 0 {
				ends[old.Row] = old.Base
			}
		}
		p.Ended[parent] = ends
	}
}

// ProjectionDiscardVersion drops every entry of version.
func ProjectionDiscardVersion(p *index.ExclusionProjection, version ids.VersionID) {
	for idx, entries := range p.Entries {
		if !slices.ContainsFunc(entries, func(e index.ExclusionEntry) bool { return e.Row.Version == version }) {
			continue
		}
		next := make([]index.ExclusionEntry, 0, len(entries)-1)
		for _, entry := range entries {
			if entry.Row.Version != version {
				next = append(next, entry)
			}
		}
		p.Entries[idx] = next
	}
}

// ProjectionAdmit marks version's entries on idx admitted.
func ProjectionAdmit(p *index.ExclusionProjection, idx ids.ObjectAddress, version ids.VersionID) {
	entries := slices.Clone(p.Entries[idx])
	for at := range entries {
		if entries[at].Row.Version == version {
			entries[at].Admitted = true
		}
	}
	p.Entries[idx] = entries
}
