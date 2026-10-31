package index

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// PgstatindexCapture borrows the real effective root and its coherent parent
// versions/ends and private projection. No predicate or expression is evaluated.
type PgstatindexCapture struct {
	Index, Parent *catalog.Relation
	Root          *storage.IndexRoot
	Store         storage.Relation
	Writes        []storage.RowVersion
	Deletes       *visibility.PrivateDeletes
	Frontier      ids.CommitSeq
}
type PgstatindexStats struct {
	Version, Level, Size, Root, Internal, Leaves, Empty, Deleted int64
	Density, Fragmentation                                       float64
	LiveItems, DeadItems, Weight                                 int64
}

// CompactBtreeLayout is a stateless aggregate-packing model, not a PostgreSQL
// page decoder. It uses 8192-byte pages, 40 bytes overhead and fanout 256.
func CompactBtreeLayout(ctx context.Context, weight int64) (PgstatindexStats, error) {
	s := PgstatindexStats{Version: 4, Size: 8192, Density: math.NaN(), Fragmentation: math.NaN(), Weight: weight}
	if err := ctx.Err(); err != nil {
		return s, err
	}
	if weight < 0 {
		_, err := storage.AddSize(weight, 0)
		return s, err
	}
	if weight == 0 {
		return s, nil
	}
	s.Leaves = weight / 8152
	if weight%8152 != 0 {
		s.Leaves++
	}
	for children := s.Leaves; children > 1; {
		if err := ctx.Err(); err != nil {
			return s, err
		}
		parents := children / 256
		if children%256 != 0 {
			parents++
		}
		var err error
		s.Internal, err = storage.AddSize(s.Internal, parents)
		if err != nil {
			return s, err
		}
		s.Level++
		children = parents
	}
	pages, err := storage.AddSize(s.Leaves, s.Internal)
	if err != nil {
		return s, err
	}
	s.Root = pages
	pages, err = storage.AddSize(pages, 1)
	if err != nil {
		return s, err
	}
	if pages > math.MaxInt64/8192 {
		_, err = storage.AddSize(math.MaxInt64, 1)
		return s, err
	}
	s.Size = pages * 8192
	s.Density = math.Round(10000*float64(weight)/(float64(s.Leaves)*8152)) / 100
	s.Fragmentation = 0
	return s, nil
}

func CapturePgstatindex(ctx context.Context, c PgstatindexCapture) (PgstatindexStats, error) {
	fail := func() (PgstatindexStats, error) {
		return PgstatindexStats{}, fmt.Errorf("stuntgres: incoherent pgstatindex capture")
	}
	if err := ctx.Err(); err != nil {
		return PgstatindexStats{}, err
	}
	if c.Index == nil || c.Parent == nil || c.Index.SQLIndex == nil || c.Index.SQLIndex.Parent != c.Parent.Address() || c.Root == nil || c.Root.Hash != nil || c.Root.Brin != nil {
		return fail()
	}
	seen := make(map[ids.VersionID]int, len(c.Root.Entries))
	replaced := make(map[ids.RowID]ids.VersionID, len(c.Writes))
	for _, r := range c.Writes {
		if err := ctx.Err(); err != nil {
			return PgstatindexStats{}, err
		}
		if r.Row == 0 || r.Version == 0 {
			return fail()
		}
		if _, ok := replaced[r.Row]; ok {
			return fail()
		}
		replaced[r.Row] = r.Base
	}
	var weight, live, dead int64
	add := func(row storage.RowVersion, keys []sqltype.Value, ends *storage.DeletionRoot) error {
		if len(keys) != len(c.Index.SQLIndex.Keys) {
			return fmt.Errorf("stuntgres: incoherent pgstatindex keys")
		}
		n, err := storage.IndexItemAllocation(ctx, c.Index, c.Parent, keys, row.Values)
		if err != nil {
			return err
		}
		weight, err = storage.AddSize(weight, n)
		if err != nil {
			return err
		}
		visible := visibility.LiveVersion(row, c.Frontier, ends, c.Deletes)
		if base, ok := replaced[row.Row]; ok && base != 0 && base == row.Version {
			visible = false
		}
		if visible {
			live, err = storage.AddSize(live, 1)
		} else {
			dead, err = storage.AddSize(dead, 1)
		}
		return err
	}
	for position, entry := range c.Root.Entries {
		if err := ctx.Err(); err != nil {
			return PgstatindexStats{}, err
		}
		if entry.Row == 0 || entry.Version == 0 {
			return fail()
		}
		if _, ok := seen[entry.Version]; ok {
			return fail()
		}
		row, found, err := pgstatindexEntryRow(ctx, c.Store.Versions, c.Writes, entry)
		if err != nil {
			return PgstatindexStats{}, err
		}
		if !found {
			return fail()
		}
		seen[entry.Version] = position
		if err := add(row, entry.Keys, c.Store.Ends); err != nil {
			return PgstatindexStats{}, err
		}
	}
	var scratch []sqltype.Value
	for _, row := range c.Writes {
		if err := ctx.Err(); err != nil {
			return PgstatindexStats{}, err
		}
		output, produced := row.IndexOutputs[c.Index.Address()]
		if produced && !OutputMatches(output, c.Index) {
			return PgstatindexStats{}, ids.ErrCatalogReplaced
		}
		if position, ok := seen[row.Version]; ok {
			entry := c.Root.Entries[position]
			if entry.Row != row.Row {
				return fail()
			}
			if produced {
				if !output.Member || len(output.Keys) != len(entry.Keys) {
					return fail()
				}
				for i, key := range entry.Keys {
					if !sqltype.SameDatum(key, output.Keys[i]) {
						return fail()
					}
				}
			} else if !RowMaintainsIndex(row, c.Index) || c.Index.SQLIndex.RequiresInsertionOutput() {
				return fail()
			}
			continue
		}
		if produced && !output.Member {
			if len(output.Keys) != 0 {
				return fail()
			}
			continue
		}
		keys := output.Keys
		if !produced {
			// Ordinary attribute-only indexes have the existing implicit row-backed
			// membership lane; the admission pin proves this index participated.
			if !RowMaintainsIndex(row, c.Index) {
				continue
			}
			if c.Index.SQLIndex.RequiresInsertionOutput() {
				return fail()
			}
			if scratch == nil {
				scratch = make([]sqltype.Value, len(c.Index.SQLIndex.Keys))
			}
			for i, key := range c.Index.SQLIndex.Keys {
				if key.Derived != nil {
					return fail()
				}
				slot := -1
				for j, attribute := range c.Parent.Attributes {
					if attribute == key.Attribute {
						slot = j
						break
					}
				}
				if slot < 0 || slot >= len(row.Values) {
					return fail()
				}
				scratch[i] = row.Values[slot]
			}
			keys = scratch
		}
		if err := add(row, keys, nil); err != nil {
			return PgstatindexStats{}, err
		}
	}
	s, err := CompactBtreeLayout(ctx, weight)
	s.LiveItems, s.DeadItems = live, dead
	return s, err
}
func pgstatindexEntryRow(ctx context.Context, versions, writes []storage.RowVersion, entry storage.IndexEntry) (storage.RowVersion, bool, error) {
	if entry.Position >= 0 && entry.Position < len(versions) {
		r := versions[entry.Position]
		if r.Row == entry.Row && r.Version == entry.Version {
			return r, true, nil
		}
	}
	for i, r := range writes {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return storage.RowVersion{}, false, err
			}
		}
		if r.Row == entry.Row && r.Version == entry.Version {
			return r, true, nil
		}
	}
	return storage.RowVersion{}, false, nil
}
