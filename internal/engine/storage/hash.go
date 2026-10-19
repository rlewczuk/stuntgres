package storage

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// HashBinding is a hash index's bound key: a column slot, or slot -1 and a
// derived expression, with its usage, operator class and hash provider.
type HashBinding struct {
	Slot       int
	Expression expr.Expr
	Derived    *catalog.DerivedIndexKey
	Usage      sqltype.Usage
	Class      sqltype.IndexClass
	Provider   sqltype.IndexHash
}

// HashEntry is one hash index entry.
type HashEntry struct {
	Key      sqltype.Value
	Row      ids.RowID
	Version  ids.VersionID
	Position int
}

// HashRoot is a hash index's buckets and the storage incarnation (identity,
// generation, prefix length and end root) they were built from.
type HashRoot struct {
	Buckets    map[uint64][]HashEntry
	RowEntries map[ids.RowID]*HashEntry
	Binding    HashBinding
	Storage    *Relation
	Generation uint64
	Prefix     int
	Ends       *DeletionRoot
}

// CaptureRowEntries indexes the entries by row for exclusion indexes with
// runtime domain key conversion; other definitions leave RowEntries unset.
func (r *HashRoot) CaptureRowEntries(ctx context.Context, d *catalog.IndexDefinition) error {
	if !d.IsExclusion() || !d.HasRuntimeDomainKeyConversion() {
		return nil
	}
	r.RowEntries = make(map[ids.RowID]*HashEntry)
	for _, bucket := range r.Buckets {
		for i := range bucket {
			if err := ctx.Err(); err != nil {
				return err
			}
			entry := &bucket[i]
			r.RowEntries[entry.Row] = entry
		}
	}
	return nil
}
