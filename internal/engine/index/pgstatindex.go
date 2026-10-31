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

// PgstattupleStatsCapture borrows an effective immutable root. B-tree captures
// also borrow coherent parent versions/ends and private outputs; hash uses only
// bucket lengths. Neither path evaluates predicates or expressions.
type PgstattupleStatsCapture struct {
	Index, Parent *catalog.Relation
	Root          *storage.IndexRoot
	Store         storage.Relation
	Writes        []storage.RowVersion
	Deletes       *visibility.PrivateDeletes
	Frontier      ids.CommitSeq
}
type PgstattupleStatsResult struct {
	Version, Level, Size, Root, Internal, Leaves, Empty, Deleted int64
	Density, Fragmentation                                       float64
	LiveItems, DeadItems, Weight                                 int64
	Buckets, Overflow, Bitmap, Unused int64
	FreePercent float64
}

// CompactBtreeLayout is a stateless aggregate-packing model, not a PostgreSQL
// page decoder. It uses 8192-byte pages, 40 bytes overhead and fanout 256.
func CompactBtreeLayout(ctx context.Context, weight int64) (PgstattupleStatsResult, error) {
	s := PgstattupleStatsResult{Version: 4, Size: 8192, Density: math.NaN(), Fragmentation: math.NaN(), Weight: weight}
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

func CapturePgstattupleStats(ctx context.Context, c PgstattupleStatsCapture) (PgstattupleStatsResult, error) {
	fail := func() (PgstattupleStatsResult, error) {
		return PgstattupleStatsResult{}, fmt.Errorf("stuntgres: incoherent pgstattuple statistics capture")
	}
	if err := ctx.Err(); err != nil {
		return PgstattupleStatsResult{}, err
	}
	if c.Index == nil || c.Parent == nil || c.Index.SQLIndex == nil || c.Index.SQLIndex.Parent != c.Parent.Address() || c.Root == nil || c.Root.Brin != nil {
		return fail()
	}
	if c.Index.SQLIndex.MethodIdentity() == catalog.HashMethod.OID() {
		if c.Root.Hash == nil {
			return fail()
		}
		var count int64
		buckets := 0
		for _, bucket := range c.Root.Hash.Buckets {
			if buckets%64 == 0 {
				if err := ctx.Err(); err != nil {
					return PgstattupleStatsResult{}, err
				}
			}
			var err error
			count, err = storage.AddSize(count, int64(len(bucket)))
			if err != nil {
				return PgstattupleStatsResult{}, err
			}
			buckets++
		}
		return CompactHashLayout(ctx, count, 0)
	}
	if c.Root.Hash != nil || c.Index.SQLIndex.MethodIdentity() != catalog.BtreeMethod.OID() {
		return fail()
	}
	hint := len(c.Root.Entries)
	seen := make(map[ids.VersionID]int, hint)
	replaced := make(map[ids.RowID]ids.VersionID, len(c.Writes))
	private := make(map[ids.VersionID]storage.RowVersion, len(c.Writes))
	for _, row := range c.Writes {
		if err := ctx.Err(); err != nil {
			return PgstattupleStatsResult{}, err
		}
		if row.Row == 0 || row.Version == 0 {
			return fail()
		}
		if _, ok := replaced[row.Row]; ok {
			return fail()
		}
		if _, ok := private[row.Version]; ok {
			return fail()
		}
		replaced[row.Row], private[row.Version] = row.Base, row
	}
	var weight, live, dead int64
	add := func(row storage.RowVersion, keys []sqltype.Value, ends *storage.DeletionRoot) error {
		if len(keys) != len(c.Index.SQLIndex.Keys) {
			return fmt.Errorf("stuntgres: incoherent pgstattuple statistics keys")
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
	resident := func(entryIndex int, rowID ids.RowID, version ids.VersionID, position int, keys []sqltype.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if rowID == 0 || version == 0 {
			return fmt.Errorf("stuntgres: invalid statistics resident membership")
		}
		if _, exists := seen[version]; exists {
			return fmt.Errorf("stuntgres: duplicate statistics resident version")
		}
		var row storage.RowVersion
		if position >= 0 && position < len(c.Store.Versions) && c.Store.Versions[position].Row == rowID && c.Store.Versions[position].Version == version {
			row = c.Store.Versions[position]
		} else if own, ok := private[version]; ok && own.Row == rowID {
			row = own
		} else {
			return fmt.Errorf("stuntgres: invalid statistics resident position")
		}
		seen[version] = entryIndex
		return add(row, keys, c.Store.Ends)
	}
	for i, entry := range c.Root.Entries {
		if err := resident(i, entry.Row, entry.Version, entry.Position, entry.Keys); err != nil {
			return PgstattupleStatsResult{}, err
		}
	}
	var scratch []sqltype.Value
	for _, row := range c.Writes {
		if err := ctx.Err(); err != nil {
			return PgstattupleStatsResult{}, err
		}
		output, produced := row.IndexOutputs[c.Index.Address()]
		if produced && !OutputMatches(output, c.Index) {
			return PgstattupleStatsResult{}, ids.ErrCatalogReplaced
		}
		if entryIndex, ok := seen[row.Version]; ok {
			entry := &c.Root.Entries[entryIndex]
			if entry.Row != row.Row {
				return fail()
			}
			if produced {
				if !output.Member {
					return fail()
				}
				if len(output.Keys) != len(entry.Keys) {
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
			// The existing attribute-only lane is authorized by the actual
			// admission pin. Derived keys and predicates require producer output.
			if !RowMaintainsIndex(row, c.Index) {
				continue
			}
			if c.Index.SQLIndex.RequiresInsertionOutput() {
				return fail()
			}
			if scratch == nil {
				scratch = make([]sqltype.Value, len(c.Index.SQLIndex.Keys))
			}
			for i, definition := range c.Index.SQLIndex.Keys {
				slot := -1
				for j, attribute := range c.Parent.Attributes {
					if attribute == definition.Attribute {
						slot = j
						break
					}
				}
				if definition.Derived != nil || slot < 0 || slot >= len(row.Values) {
					return fail()
				}
				scratch[i] = row.Values[slot]
			}
			keys = scratch
		}
		if err := add(row, keys, nil); err != nil {
			return PgstattupleStatsResult{}, err
		}
	}
	stats, err := CompactBtreeLayout(ctx, weight)
	stats.LiveItems, stats.DeadItems = live, dead
	return stats, err
}

// CompactHashLayout models fixed-size hash tuples, not PostgreSQL split history.
// Four base buckets, one bitmap, 8152 usable bytes/page and 20 bytes/item are
// explicit conventions. Its arithmetic is constant-space and allocation-free.
func CompactHashLayout(ctx context.Context, live, dead int64) (PgstattupleStatsResult, error) {
	var stats PgstattupleStatsResult
	if err := ctx.Err(); err != nil {
		return stats, err
	}
	count, err := storage.AddSize(live, dead)
	if err != nil {
		return stats, err
	}
	pages := count / 407
	if count%407 != 0 {
		pages++
	}
	if pages < 4 {
		pages = 4
	}
	if count > math.MaxInt64/20 || pages > math.MaxInt64/8152 {
		_, err := storage.AddSize(math.MaxInt64, 1)
		return stats, err
	}
	weight, capacity := 20*count, 8152*pages
	return PgstattupleStatsResult{Version: 4, Buckets: 4, Overflow: pages-4, Bitmap: 1, LiveItems: live, DeadItems: dead, Weight: weight, FreePercent: 100*float64(capacity-weight)/float64(capacity)}, nil
}
