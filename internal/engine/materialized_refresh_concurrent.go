package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func materializedIndexEligible(parent, index *relationDescriptor) bool {
	if parent == nil || parent.relationKind != materializedViewRelation || index == nil || index.relationKind != indexRelation || index.sqlIndex == nil {
		return false
	}
	d := index.sqlIndex
	if d.parent != parent.address || !d.unique || !d.valid || !d.ready || !d.live || d.predicate != nil || len(d.keys) == 0 {
		return false
	}
	for _, key := range d.keys {
		if key.attribute <= 0 || key.derived != nil || !parent.hasAttribute(key.attribute) {
			return false
		}
	}
	for _, attribute := range d.include {
		if attribute <= 0 || !parent.hasAttribute(attribute) {
			return false
		}
	}
	return true
}

func materializedConcurrentAdmission(view CatalogRead, parent *relationDescriptor, noData bool) error {
	if !parent.populated {
		return sqlError("0A000", "CONCURRENTLY cannot be used when the materialized view is not populated")
	}
	if noData {
		return sqlError("42601", "CONCURRENTLY and WITH NO DATA options cannot be used together")
	}
	for _, descriptor := range view.state.descriptors {
		if index, ok := descriptor.(*relationDescriptor); ok && materializedIndexEligible(parent, index) {
			return nil
		}
	}
	namespace := view.state.descriptors[parent.namespace].(*namespaceDescriptor)
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf(`cannot refresh materialized view "%s.%s" concurrently`, pgIdentifier(namespace.name), pgIdentifier(parent.name)), Hint: "Create a unique index with no WHERE clause on one or more columns of the materialized view."}
}

func materializedEligibleIndexes(view CatalogRead, parent *relationDescriptor) []*relationDescriptor {
	var indices []*relationDescriptor
	for _, descriptor := range view.state.descriptors {
		if index, ok := descriptor.(*relationDescriptor); ok && materializedIndexEligible(parent, index) {
			indices = append(indices, index)
		}
	}
	return indices
}

func materializedSameRow(a, b []sqltype.Value) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if !sqltype.SameDatum(v, b[i]) {
			return false
		}
	}
	return true
}

func materializedNewEntries(ctx context.Context, ordering indexOrdering, rows [][]sqltype.Value) ([]logicalIndexEntry, error) {
	entries := make([]logicalIndexEntry, len(rows))
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1}
	for i, values := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry, err := ordering.entry(rowVersion{values: values, row: RowID(i + 1), version: VersionID(i + 1)}, &evaluation)
		if err != nil {
			return nil, err
		}
		entry.position = i
		entries[i] = entry
	}
	if err := ordering.sort(ctx, entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func materializedGroupEnd(ctx context.Context, ordering indexOrdering, entries []logicalIndexEntry, start int) (int, error) {
	end := start + 1
	for end < len(entries) {
		cmp, err := ordering.compareKeys(ctx, entries[start], entries[end])
		if err != nil {
			return 0, err
		}
		if cmp != 0 {
			break
		}
		end++
	}
	return end, nil
}

func materializedDuplicatePreflight(ctx context.Context, view CatalogRead, parent *relationDescriptor, ordering indexOrdering, entries []logicalIndexEntry, rows [][]sqltype.Value, output sqltype.OutputEnvironment) error {
	duplicate := len(rows)
	for start := 0; start < len(entries) && duplicate != 0; {
		end, err := materializedGroupEnd(ctx, ordering, entries, start)
		if err != nil {
			return err
		}
		for i := start; i < end && duplicate != 0; i++ {
			a := entries[i].position
			if uniqueHasNull(rows[a]) {
				continue
			}
			for j := i + 1; j < end && duplicate != 0; j++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				b := entries[j].position
				candidate := min(a, b)
				if candidate < duplicate && materializedSameRow(rows[a], rows[b]) {
					duplicate = candidate
				}
			}
		}
		start = end
	}
	if duplicate == len(rows) {
		return nil
	}
	typ, ok := view.LookupType(parent.rowType)
	if !ok {
		return fmt.Errorf("stuntgres: materialized row type is missing")
	}
	value, err := sqltype.OwnedCompositeValue(typ, rows[duplicate])
	if err != nil {
		return err
	}
	text, err := typ.OutputWithEnvironment(ctx, value, output)
	if err != nil {
		return err
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "21000", Message: fmt.Sprintf(`new data for materialized view "%s" contains duplicate rows without any null columns`, parent.name), Detail: "Row: " + text}
}

func materializedRowsMatch(ctx context.Context, orderings []indexOrdering, old, new []sqltype.Value) (bool, error) {
	for _, ordering := range orderings {
		for i, slot := range ordering.slots {
			a, b := old[slot], new[slot]
			if a.IsNull() || b.IsNull() {
				return false, nil
			}
			cmp, err := ordering.comparisons[i].Compare(ctx, a, b, sqltype.InputEnvironment{})
			if err != nil {
				return false, err
			}
			if cmp != 0 {
				return false, nil
			}
		}
	}
	return materializedSameRow(old, new), nil
}

// Called outside Engine.mu with the captured immutable predecessor roots. One
// eligible key narrows candidates; all eligible key equalities and row images
// decide matches. Matches are existential, never consumed one-to-one. Datum
// buffers are shared/transferred, including each surviving old row's image.
func materializedConcurrentRows(ctx context.Context, view CatalogRead, parent *relationDescriptor, previous *relationStorage, frontier CommitSeq, oldEntries []logicalIndexEntry, indices []*relationDescriptor, rows [][]sqltype.Value, output sqltype.OutputEnvironment) ([][]sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(indices) == 0 {
		return nil, fmt.Errorf("stuntgres: concurrent materialized index authority is missing")
	}
	orderings := make([]indexOrdering, len(indices))
	for i, index := range indices {
		var err error
		orderings[i], err = bindIndexOrdering(parent, index.sqlIndex)
		if err != nil {
			return nil, err
		}
	}
	ordering := orderings[0]
	entries, err := materializedNewEntries(ctx, ordering, rows)
	if err != nil {
		return nil, err
	}
	if err := materializedDuplicatePreflight(ctx, view, parent, ordering, entries, rows, output); err != nil {
		return nil, err
	}
	if len(rows) == 0 || len(previous.versions) == 0 {
		return rows, nil
	}
	var matched []bool
	var effective [][]sqltype.Value
	for oldAt, newAt := 0, 0; oldAt < len(oldEntries) && newAt < len(entries); {
		cmp, err := ordering.compareKeys(ctx, oldEntries[oldAt], entries[newAt])
		if err != nil {
			return nil, err
		}
		if cmp < 0 {
			oldAt++
			continue
		}
		if cmp > 0 {
			newAt++
			continue
		}
		oldEnd, err := materializedGroupEnd(ctx, ordering, oldEntries, oldAt)
		if err != nil {
			return nil, err
		}
		newEnd, err := materializedGroupEnd(ctx, ordering, entries, newAt)
		if err != nil {
			return nil, err
		}
		// SQL key equality never matches NULL, even for NULLS NOT DISTINCT.
		// Equal selected-key groups share their NULL positions, so every old
		// occurrence is deleted and every producer occurrence stays unmatched.
		if uniqueHasNull(entries[newAt].keys) {
			oldAt, newAt = oldEnd, newEnd
			continue
		}
		for i := oldAt; i < oldEnd; i++ {
			if !liveVersion(previous.versions[oldEntries[i].position], frontier, previous.ends, nil) {
				continue
			}
			values := previous.versions[oldEntries[i].position].values
			survives := false
			for j := newAt; j < newEnd; j++ {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				at := entries[j].position
				match, err := materializedRowsMatch(ctx, orderings, values, rows[at])
				if err != nil {
					return nil, err
				}
				if match {
					if matched == nil {
						matched = make([]bool, len(rows))
						effective = make([][]sqltype.Value, 0, len(oldEntries)+len(rows))
					}
					matched[at] = true
					survives = true
				}
			}
			if survives {
				effective = append(effective, values)
			}
		}
		oldAt, newAt = oldEnd, newEnd
	}
	if matched == nil {
		return rows, nil
	}
	for i, values := range rows {
		if !matched[i] {
			effective = append(effective, values)
		}
	}
	return effective, nil
}

// Metadata tokens are accessed only under Engine.mu. They retain no storage
// or row vector; a nonconcurrent rewrite/control replacement breaks provenance.
type materializedLineage struct{ marker byte }

func sameMaterializedLineage(old, current *relationDescriptor, a, b *relationStorage) bool {
	return old != nil && current != nil && old.relationKind == materializedViewRelation && current.relationKind == materializedViewRelation && old.populated && current.populated && a != nil && b != nil && a.materializedLineage != nil && a.materializedLineage == b.materializedLineage && relationDefiningAuthorityCompatible(old, current)
}

// A producer snapshot may outlive a compatible source publication, never a
// defining-authority change. All storage/token observations require Engine.mu.
func materializedSourceCurrent(snapshot *visibilityData, observed Descriptor, current *catalogState, store *relationStorage) bool {
	if current.descriptors[observed.Address()] == observed {
		return true
	}
	if snapshot == nil || snapshot.catalog.Epoch() != current.epoch || snapshot.catalog.state.descriptors[observed.Address()] != observed {
		return false
	}
	old, ok := observed.(*relationDescriptor)
	if !ok {
		return false
	}
	live, _ := current.descriptors[observed.Address()].(*relationDescriptor)
	return sameMaterializedLineage(old, live, snapshot.generationAt(observed.Address()), store)
}

type materializedSourceFence struct {
	ref      ids.ObjectRef
	relation *relationDescriptor
	storage  *relationStorage
}

func (e *Engine) materializedSourceFenceCurrent(f materializedSourceFence) bool {
	if f.ref.Domain() != e.domain || f.ref.Epoch != e.state.epoch || f.relation == nil || f.storage == nil || f.ref.Address != f.relation.address || f.relation.relationKind != materializedViewRelation {
		return false
	}
	current, _ := e.state.descriptors[f.ref.Address].(*relationDescriptor)
	store := e.storage[f.ref.Address]
	return current == f.relation && store == f.storage || sameMaterializedLineage(f.relation, current, f.storage, store)
}
