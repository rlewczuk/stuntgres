package view

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// ConcurrentPrevious is the previous contents of a concurrently refreshed
// materialized view as the refreshing transaction sees them: the committed
// versions live at frontier and the transaction's own versions, both without
// its own deletes. The deletion root is folded in and cleared.
func ConcurrentPrevious(previous storage.Relation, own []storage.RowVersion, deletes *visibility.PrivateDeletes, frontier ids.CommitSeq) storage.Relation {
	versions := make([]storage.RowVersion, 0, len(previous.Versions)+len(own))
	for _, row := range previous.Versions {
		if visibility.LiveVersion(row, frontier, previous.Ends, deletes) {
			versions = append(versions, row)
		}
	}
	for _, row := range own {
		if visibility.LiveVersion(row, frontier, nil, deletes) {
			versions = append(versions, row)
		}
	}
	previous.Versions, previous.Ends = versions, nil
	return previous
}

// ValidateMaterializedReplacement checks a staged REFRESH replacement r
// against stage: the staged descriptor is r's private copy of the original,
// with only its revision and population changed, and the new storage is a
// fresh generation of well-typed rows. A nil r is valid.
func ValidateMaterializedReplacement(r *MaterializedReplacement, stage *catalog.Candidate) error {
	if r == nil {
		return nil
	}
	if r.Original == nil || r.Descriptor == nil {
		return fmt.Errorf("stuntgres: invalid materialized replacement")
	}
	d, ok := stage.Get(r.Original.Address()).(*catalog.Relation)
	if !ok || r.Original.RelationKind != catalog.MaterializedViewRelation || stage.Base.Get(r.Original.Address()) != r.Original || r.Storage == nil || r.Predecessor == nil || r.Storage == r.Predecessor || r.Storage.Ends != nil || r.Storage.Generation != r.Generation+1 || r.Generation == ^uint64(0) {
		return fmt.Errorf("stuntgres: invalid materialized replacement")
	}
	// This private constructor owns the shallow descriptor copy. No general
	// Replace/ALTER caller may attach arbitrary metadata to its storage authority.
	old := r.Original
	if d != r.Descriptor || d.Address() != old.Address() || d.Namespace() != old.Namespace() || d.Name() != old.Name() || d.Owner != old.Owner || d.RowType != old.RowType || d.View != old.View || d.TableMethod != old.TableMethod || d.RelationKind != catalog.MaterializedViewRelation || d.Revision() != old.Revision()+1 || len(d.Columns) != len(old.Columns) || len(d.Attributes) != len(old.Attributes) || len(d.Columns) != 0 && &d.Columns[0] != &old.Columns[0] || len(d.Attributes) != 0 && &d.Attributes[0] != &old.Attributes[0] || !d.Populated && len(r.Storage.Versions) != 0 {
		return fmt.Errorf("stuntgres: invalid materialized replacement definition")
	}
	if r.Concurrent && !d.Populated {
		return fmt.Errorf("stuntgres: concurrent replacement is unpopulated")
	}
	for _, row := range r.Storage.Versions {
		if row.Row == 0 || row.Version == 0 || row.Born != 0 || len(row.Values) != len(d.Columns) {
			return fmt.Errorf("stuntgres: invalid materialized replacement row")
		}
		for i, v := range row.Values {
			if v.Type() != d.Columns[i].Usage.Type {
				return fmt.Errorf("stuntgres: invalid materialized replacement value")
			}
		}
	}
	return nil
}

// Current checks that the replacement r staged in stage still applies to
// snapshot and the stored relation: the original descriptor, its storage
// generation and length, and the exact set of its indexes are unchanged.
// Anything else is a catalog.Conflict. A nil r is current.
func (r *MaterializedReplacement) Current(stage *catalog.Candidate, snapshot *catalog.State, stored *storage.Relation) error {
	if r == nil {
		return nil
	}
	if stage.Get(r.Original.Address()) != r.Descriptor {
		return catalog.Conflict{}
	}
	if snapshot.Epoch() != stage.Base.Epoch() || snapshot.Get(r.Original.Address()) != r.Original || stored != r.Predecessor || r.Predecessor.Generation != r.Generation || len(r.Predecessor.Versions) != r.Length || r.Predecessor.Ends != r.Ends {
		return catalog.Conflict{}
	}
	// The effective diff depends on every eligible key; root publication also
	// depends on ineligible indexes. Fence the exact original complete set,
	// without turning a replacement into a REINDEX maintenance intent.
	count := 0
	for address, descriptor := range snapshot.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != r.Original.Address() {
			continue
		}
		if stage.Base.Get(address) != descriptor {
			return catalog.Conflict{}
		}
		count++
	}
	if count != r.IndexCount {
		return catalog.Conflict{}
	}
	return nil
}

// CheckMaterializedRows checks that every refresh row is as wide as d and that
// each value has its column's type.
func CheckMaterializedRows(d *catalog.Relation, rows [][]sqltype.Value) error {
	for _, row := range rows {
		if len(row) != len(d.Columns) {
			return fmt.Errorf("stuntgres: invalid materialized row width")
		}
		for i, value := range row {
			if value.Type() != d.Columns[i].Usage.Type {
				return fmt.Errorf("stuntgres: invalid materialized value type")
			}
		}
	}
	return nil
}

// MaterializedIndexCount counts the indexes of parent in base.
func MaterializedIndexCount(base catalog.Read, parent ids.ObjectAddress) int {
	count := 0
	for _, descriptor := range base.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil && index.SQLIndex.Parent == parent {
			count++
		}
	}
	return count
}
