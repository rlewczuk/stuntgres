package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func MaterializedIndexEligible(parent, index *catalog.Relation) bool {
	if parent == nil || parent.RelationKind != catalog.MaterializedViewRelation || index == nil || index.RelationKind != catalog.IndexRelation || index.SQLIndex == nil {
		return false
	}
	d := index.SQLIndex
	if d.Parent != parent.Address() || !d.Unique || !d.Valid || !d.Ready || !d.Live || d.Predicate != nil || len(d.Keys) == 0 {
		return false
	}
	for _, key := range d.Keys {
		if key.Attribute <= 0 || key.Derived != nil || !parent.HasAttribute(key.Attribute) {
			return false
		}
	}
	for _, attribute := range d.Include {
		if attribute <= 0 || !parent.HasAttribute(attribute) {
			return false
		}
	}
	return true
}

func MaterializedConcurrentAdmission(view catalog.Read, parent *catalog.Relation, noData bool) error {
	if !parent.Populated {
		return sqlError("0A000", "CONCURRENTLY cannot be used when the materialized view is not populated")
	}
	if noData {
		return sqlError("42601", "CONCURRENTLY and WITH NO DATA options cannot be used together")
	}
	for _, descriptor := range view.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && MaterializedIndexEligible(parent, index) {
			return nil
		}
	}
	namespace := view.MustNamespace(parent.Namespace())
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf(`cannot refresh materialized view "%s.%s" concurrently`, catalog.Identifier(namespace.Name()), catalog.Identifier(parent.Name())), Hint: "Create a unique index with no WHERE clause on one or more columns of the materialized view."}
}

func MaterializedEligibleIndexes(view catalog.Read, parent *catalog.Relation) []*catalog.Relation {
	var indices []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && MaterializedIndexEligible(parent, index) {
			indices = append(indices, index)
		}
	}
	return indices
}

func MaterializedSameRow(a, b []sqltype.Value) bool {
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

func MaterializedNewEntries(ctx context.Context, ordering index.Ordering, rows [][]sqltype.Value) ([]storage.IndexEntry, error) {
	entries := make([]storage.IndexEntry, len(rows))
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	for i, values := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry, err := ordering.Entry(storage.RowVersion{Values: values, Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1)}, &evaluation)
		if err != nil {
			return nil, err
		}
		entry.Position = i
		entries[i] = entry
	}
	if err := ordering.Sort(ctx, entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func materializedGroupEnd(ctx context.Context, ordering index.Ordering, entries []storage.IndexEntry, start int) (int, error) {
	end := start + 1
	for end < len(entries) {
		cmp, err := ordering.CompareKeys(ctx, entries[start], entries[end])
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

func MaterializedDuplicatePreflight(ctx context.Context, view catalog.Read, parent *catalog.Relation, ordering index.Ordering, entries []storage.IndexEntry, rows [][]sqltype.Value, output sqltype.OutputEnvironment) error {
	duplicate := len(rows)
	for start := 0; start < len(entries) && duplicate != 0; {
		end, err := materializedGroupEnd(ctx, ordering, entries, start)
		if err != nil {
			return err
		}
		for i := start; i < end && duplicate != 0; i++ {
			a := entries[i].Position
			if index.UniqueHasNull(rows[a]) {
				continue
			}
			for j := i + 1; j < end && duplicate != 0; j++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				b := entries[j].Position
				candidate := min(a, b)
				if candidate < duplicate && MaterializedSameRow(rows[a], rows[b]) {
					duplicate = candidate
				}
			}
		}
		start = end
	}
	if duplicate == len(rows) {
		return nil
	}
	typ, ok := view.LookupType(parent.RowType)
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
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "21000", Message: fmt.Sprintf(`new data for materialized view "%s" contains duplicate rows without any null columns`, parent.Name()), Detail: "Row: " + text}
}

func materializedRowsMatch(ctx context.Context, orderings []index.Ordering, old, new []sqltype.Value) (bool, error) {
	for _, ordering := range orderings {
		for i, slot := range ordering.Slots {
			a, b := old[slot], new[slot]
			if a.IsNull() || b.IsNull() {
				return false, nil
			}
			cmp, err := ordering.Comparisons[i].Compare(ctx, a, b, sqltype.InputEnvironment{})
			if err != nil {
				return false, err
			}
			if cmp != 0 {
				return false, nil
			}
		}
	}
	return MaterializedSameRow(old, new), nil
}

// Called outside Engine.mu with the captured immutable predecessor roots. One
// eligible key narrows candidates; all eligible key equalities and row images
// decide matches. Matches are existential, never consumed one-to-one. Datum
// buffers are shared/transferred, including each surviving old row's image.
func MaterializedConcurrentRows(ctx context.Context, view catalog.Read, parent *catalog.Relation, previous *storage.Relation, frontier ids.CommitSeq, oldEntries []storage.IndexEntry, indices []*catalog.Relation, rows [][]sqltype.Value, output sqltype.OutputEnvironment) ([][]sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(indices) == 0 {
		return nil, fmt.Errorf("stuntgres: concurrent materialized index authority is missing")
	}
	orderings := make([]index.Ordering, len(indices))
	for i, idx := range indices {
		var err error
		orderings[i], err = index.BindIndexOrdering(parent, idx.SQLIndex)
		if err != nil {
			return nil, err
		}
	}
	ordering := orderings[0]
	entries, err := MaterializedNewEntries(ctx, ordering, rows)
	if err != nil {
		return nil, err
	}
	if err := MaterializedDuplicatePreflight(ctx, view, parent, ordering, entries, rows, output); err != nil {
		return nil, err
	}
	if len(rows) == 0 || len(previous.Versions) == 0 {
		return rows, nil
	}
	var matched []bool
	var effective [][]sqltype.Value
	for oldAt, newAt := 0, 0; oldAt < len(oldEntries) && newAt < len(entries); {
		cmp, err := ordering.CompareKeys(ctx, oldEntries[oldAt], entries[newAt])
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
		if index.UniqueHasNull(entries[newAt].Keys) {
			oldAt, newAt = oldEnd, newEnd
			continue
		}
		for i := oldAt; i < oldEnd; i++ {
			if !visibility.LiveVersion(previous.Versions[oldEntries[i].Position], frontier, previous.Ends, nil) {
				continue
			}
			values := previous.Versions[oldEntries[i].Position].Values
			survives := false
			for j := newAt; j < newEnd; j++ {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				at := entries[j].Position
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

func SameMaterializedLineage(old, current *catalog.Relation, a, b *storage.Relation) bool {
	return old != nil && current != nil && old.RelationKind == catalog.MaterializedViewRelation && current.RelationKind == catalog.MaterializedViewRelation && old.Populated && current.Populated && a != nil && b != nil && a.MaterializedLineage != nil && a.MaterializedLineage == b.MaterializedLineage && catalog.RelationDefiningAuthorityCompatible(old, current)
}

// A producer snapshot may outlive a compatible source publication, never a
// defining-authority change. All storage/token observations require Engine.mu.
func MaterializedSourceCurrent(snapshot *visibility.Data, observed catalog.Descriptor, current *catalog.State, store *storage.Relation) bool {
	if current.Get(observed.Address()) == observed {
		return true
	}
	if snapshot == nil || snapshot.Catalog.Epoch() != current.Epoch() || snapshot.Catalog.Get(observed.Address()) != observed {
		return false
	}
	old, ok := observed.(*catalog.Relation)
	if !ok {
		return false
	}
	live, _ := current.Relation(observed.Address())
	return SameMaterializedLineage(old, live, snapshot.GenerationAt(observed.Address()), store)
}
