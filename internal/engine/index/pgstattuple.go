package index

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// CaptureTupleStatistics estimates physical metrics from current logical items
// and already-produced private outputs. There is no physical index archive or
// killed-item marker; published logical removal stops contributing immediately.
func CaptureTupleStatistics(ctx context.Context, c PgstattupleStatsCapture, pagesOnly bool) (storage.HeapStatistics, error) {
	if err := ctx.Err(); err != nil { return storage.HeapStatistics{}, err }
	if c.Index == nil || c.Index.SQLIndex == nil || c.Parent == nil || c.Root == nil { return storage.HeapStatistics{}, fmt.Errorf("stuntgres: incoherent tuple index capture") }
	if pagesOnly && c.Root.Brin != nil {
		allocation, err := (storage.Allocation{Relation:c.Index, Parent:c.Parent, Allocated:true, Index:c.Root, ParentVersions:c.Store.Versions, ParentOwn:c.Writes}).Forks(ctx)
		if err != nil { return storage.HeapStatistics{}, err }
		pages := allocation.Main/8192
		if allocation.Main%8192 != 0 { pages++ }
		pages = max(int64(1), pages)
		if pages > math.MaxInt64/8192 { _, err = storage.AddSize(math.MaxInt64,1); return storage.HeapStatistics{}, err }
		return storage.HeapStatistics{TableLen:pages*8192}, nil
	}
	seen := make(map[ids.VersionID]bool)
	var count, weight int64
	add := func(row storage.RowVersion, keys []sqltype.Value) error {
		if seen[row.Version] { return nil }
		seen[row.Version] = true
		n, err := storage.IndexItemAllocation(ctx, c.Index, c.Parent, keys, row.Values)
		if err != nil { return err }
		weight, err = storage.AddSize(weight, n)
		if err != nil { return err }
		count, err = storage.AddSize(count, 1)
		return err
	}
	for _, entry := range c.Root.Entries {
		if err := ctx.Err(); err != nil { return storage.HeapStatistics{}, err }
		row, _, ok := storage.RowByID(c.Store.Versions, entry.Row, entry.Position)
		if !ok || row.Version != entry.Version {
			row, _, ok = storage.RowByID(c.Writes, entry.Row, -1)
		}
		if !ok || row.Version != entry.Version { return storage.HeapStatistics{}, fmt.Errorf("stuntgres: missing index item version") }
		if err := add(row, entry.Keys); err != nil { return storage.HeapStatistics{}, err }
	}
	if c.Root.Hash != nil {
		for _, bucket := range c.Root.Hash.Buckets {
			for _, entry := range bucket {
				if err := ctx.Err(); err != nil { return storage.HeapStatistics{}, err }
				row, _, ok := storage.RowByID(c.Store.Versions, entry.Row, entry.Position)
				if !ok || row.Version != entry.Version {
					row, _, ok = storage.RowByID(c.Writes, entry.Row, -1)
				}
				if !ok || row.Version != entry.Version { return storage.HeapStatistics{}, fmt.Errorf("stuntgres: missing hash item version") }
				keys := [1]sqltype.Value{entry.Key}
				if err := add(row, keys[:]); err != nil { return storage.HeapStatistics{}, err }
			}
		}
	}
	var scratch []sqltype.Value
	private := func(row storage.RowVersion) error {
		if err := ctx.Err(); err != nil { return err }
		if seen[row.Version] || row.Born != 0 { return nil }
		output, produced := row.IndexOutputs[c.Index.Address()]
		if produced && !OutputMatches(output, c.Index) { return ids.ErrCatalogReplaced }
		if produced && !output.Member { return nil }
		keys := output.Keys
		var hashKey [1]sqltype.Value
		if c.Root.Hash != nil && produced { hashKey[0] = output.Key; keys = hashKey[:] }
		if !produced {
			if !RowMaintainsIndex(row, c.Index) { return nil }
			if c.Index.SQLIndex.RequiresInsertionOutput() { return fmt.Errorf("stuntgres: missing private index output") }
			if scratch == nil { scratch = make([]sqltype.Value, len(c.Index.SQLIndex.Keys)) }
			for i, key := range c.Index.SQLIndex.Keys {
				slot := -1
				for j, attribute := range c.Parent.Attributes { if attribute == key.Attribute { slot = j; break } }
				if slot < 0 || slot >= len(row.Values) { return fmt.Errorf("stuntgres: missing index attribute") }
				scratch[i] = row.Values[slot]
			}
			keys = scratch
		}
		return add(row, keys)
	}
	for _, row := range c.Writes {
		if err := private(row); err != nil { return storage.HeapStatistics{}, err }
	}
	if count > math.MaxInt64/4 { _, err := storage.AddSize(math.MaxInt64,1); return storage.HeapStatistics{}, err }
	payload, err := storage.AddSize(weight, count*4)
	if err != nil { return storage.HeapStatistics{}, err }
	layout, err := CompactBtreeLayout(ctx, payload)
	if err != nil { return storage.HeapStatistics{}, err }
	pages := layout.Size/8192
	dataPages := layout.Leaves
	method := c.Index.SQLIndex.MethodIdentity()
	if method == 783 {
		// Model GiST without a metadata page. Root-only trees contribute no
		// scanned leaf items; larger trees use the compact layout estimate.
		if payload <= 8152 { count, weight, payload, pages, dataPages = 0, 0, 0, 1, 0 } else { pages-- }
	}
	if method == 405 {
		// Hash reserves a metadata page and at least two bucket pages.
		dataPages = payload/8152
		if payload%8152 != 0 { dataPages++ }
		dataPages = max(int64(2), dataPages)
		pages = dataPages+1
	}
	if pages > math.MaxInt64/8192 { _, err = storage.AddSize(math.MaxInt64,1); return storage.HeapStatistics{}, err }
	stats := storage.HeapStatistics{TableLen:pages*8192, TupleCount:count, TupleLen:weight}
	if pagesOnly { return stats, nil }
	stats.FreeSpace = max(int64(0), dataPages*8152-payload)
	if stats.TableLen != 0 {
		stats.TuplePercent = math.Round(10000*float64(weight)/float64(stats.TableLen))/100
		stats.FreePercent = math.Round(10000*float64(stats.FreeSpace)/float64(stats.TableLen))/100
	}
	return stats, nil
}
