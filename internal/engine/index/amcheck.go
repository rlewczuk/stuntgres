package index

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type AmcheckOptions struct{ Parent, HeapAllIndexed, RootDescend, CheckUnique bool }

// AmcheckCapture borrows one coherent immutable baseline and its real private
// write overlays. Own rows are not a second, invented ordered B-tree root.
type AmcheckCapture struct {
	Index, Parent       *catalog.Relation
	IndexRef, ParentRef ids.ObjectRef
	Root                *storage.IndexRoot
	Store               storage.Relation
	Frontier            ids.CommitSeq
	Writes              []storage.RowVersion
	Deletes             *visibility.PrivateDeletes
}

func amcheckError(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func (c *AmcheckCapture) corrupt(kind string) error {
	return amcheckError("XX002", fmt.Sprintf("%s for index \"%s\"", kind, c.Index.Name()))
}
func (c *AmcheckCapture) missing(row storage.RowVersion, parent bool) error {
	block, offset, err := exec.PgrowlocksLogicalTID(row.Version)
	if err != nil {
		return err
	}
	d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX001", Message: fmt.Sprintf("heap tuple (%d,%d) from table \"%s\" lacks matching index tuple within index \"%s\"", block, offset, c.Parent.Name(), c.Index.Name())}
	if !parent {
		d.Hint = "Retrying verification using the function bt_index_parent_check() might provide a more specific error."
	}
	return d
}

// CheckBtree inspects the existing vector before any recipe evaluation. It
// never rebuilds, sorts, repairs or publishes the captured baseline.
func CheckBtree(evaluation *exec.Context, c AmcheckCapture, options AmcheckOptions) error {
	ctx := evaluation.Context
	if err := ctx.Err(); err != nil {
		return err
	}
	view := evaluation.Catalog
	if c.Index == nil || c.Parent == nil || c.Index.SQLIndex == nil || view.Owner() == nil || c.IndexRef.Domain() != view.Owner().Domain || c.ParentRef.Domain() != view.Owner().Domain || c.IndexRef.Epoch != view.Epoch() || c.ParentRef.Epoch != view.Epoch() || c.IndexRef.Address != c.Index.Address() || c.ParentRef.Address != c.Parent.Address() || view.Get(c.IndexRef.Address) != c.Index || view.Get(c.ParentRef.Address) != c.Parent || c.Index.SQLIndex.Parent != c.Parent.Address() {
		return ids.ErrCatalogReplaced
	}
	if c.Root == nil || c.Root.Hash != nil {
		return c.corrupt("item order invariant violated")
	}
	d := c.Index.SQLIndex
	o, err := BindIndexOrdering(evaluation.Catalog, c.Parent, d)
	if err != nil {
		return err
	}
	entries := c.Root.Entries
	positions := make(map[ids.VersionID]int, len(entries))
	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !c.keyShape(entry.Keys) || entry.Row == 0 || entry.Version == 0 || entry.Position < 0 || entry.Position >= len(c.Store.Versions) {
			return c.corrupt("item order invariant violated")
		}
		row := c.Store.Versions[entry.Position]
		if !c.rowShape(row) {
			return c.corrupt("item order invariant violated")
		}
		if row.Row != entry.Row || row.Version != entry.Version {
			return c.corrupt("item order invariant violated")
		}
		if _, exists := positions[entry.Version]; exists {
			return c.corrupt("item order invariant violated")
		}
		positions[entry.Version] = i
		if i != 0 {
			order, err := o.compare(ctx, entries[i-1], entry)
			if err != nil {
				return err
			}
			if order >= 0 {
				return c.corrupt("item order invariant violated")
			}
		}
	}
	if options.Parent {
		if err := c.checkMaps(ctx, positions); err != nil {
			return err
		}
	}
	if options.Parent && options.RootDescend {
		if err := c.checkDescent(ctx, o.compare); err != nil {
			return err
		}
	}
	// Replacement rows suppress their baseline predecessors independently of
	// committed visibility. The original captured root remains unchanged.
	if err := c.checkPrivateAssociations(ctx); err != nil {
		return err
	}
	var replaced map[ids.RowID]ids.VersionID
	if len(c.Writes) != 0 {
		replaced = make(map[ids.RowID]ids.VersionID, len(c.Writes))
	}
	for _, row := range c.Writes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !c.rowShape(row) {
			return c.corrupt("item order invariant violated")
		}
		if row.Row == 0 || row.Version == 0 {
			return c.corrupt("item order invariant violated")
		}
		if _, exists := replaced[row.Row]; exists {
			return c.corrupt("item order invariant violated")
		}
		replaced[row.Row] = row.Base
	}
	live := func(row storage.RowVersion) bool {
		if base, exists := replaced[row.Row]; exists && base != 0 && base == row.Version {
			return false
		}
		return visibility.LiveVersion(row, c.Frontier, c.Store.Ends, c.Deletes)
	}
	if options.CheckUnique && d.Unique {
		var previous *storage.IndexEntry
		for i := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			entry := &entries[i]
			if !live(c.Store.Versions[entry.Position]) || amcheckNullKey(entry.Keys) {
				continue
			}
			if previous != nil && previous.Row != entry.Row {
				equal, err := o.CompareKeys(ctx, *previous, *entry)
				if err != nil {
					return err
				}
				if equal == 0 {
					return c.corrupt("index uniqueness is violated")
				}
			}
			previous = entry
		}
	}
	var preparation ExecutionPreparation
	var keys []sqltype.Value
	checkHeap := func(row storage.RowVersion, actual []sqltype.Value, member bool) error {
		matches, err := preparation.PredicateMatches(evaluation, c.Parent, d, row.Values, &o, nil)
		if err != nil || !matches {
			return err
		}
		if err := preparation.PrepareKeys(evaluation, c.Parent, d, &o, nil); err != nil {
			return err
		}
		if keys == nil {
			keys = make([]sqltype.Value, len(d.Keys))
		}
		evaluation.Row = row.Values
		if err := o.FillKeys(row.Values, keys, evaluation); err != nil {
			return err
		}
		if !member || !amcheckSameKeys(keys, actual) {
			return c.missing(row, options.Parent)
		}
		return nil
	}
	if options.HeapAllIndexed {
		for _, row := range c.Store.Versions {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !live(row) {
				continue
			}
			if !c.rowShape(row) {
				return c.corrupt("item order invariant violated")
			}
			position, member := positions[row.Version]
			member = member && entries[position].Row == row.Row
			var actual []sqltype.Value
			if member {
				actual = entries[position].Keys
			}
			if err := checkHeap(row, actual, member); err != nil {
				return err
			}
		}
	}
	// Only uniqueness needs retained overlay keys. Sorting this demanded scratch
	// list is not root verification: order/search always use the actual root.
	var overlay []storage.IndexEntry
	for _, row := range c.Writes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !visibility.LiveVersion(row, c.Frontier, nil, c.Deletes) {
			continue
		}
		output, produced := row.IndexOutputs[c.Index.Address()]
		if produced && !OutputMatches(output, c.Index) {
			return ids.ErrCatalogReplaced
		}
		member, actual := output.Member, output.Keys
		if produced && !member && len(actual) != 0 {
			return c.corrupt("item order invariant violated")
		}
		if !produced {
			if !RowMaintainsIndex(row, c.Index) && !options.HeapAllIndexed {
				return c.corrupt("item order invariant violated")
			}
			if !RowMaintainsIndex(row, c.Index) || d.RequiresInsertionOutput() {
				if options.HeapAllIndexed {
					if err := checkHeap(row, nil, false); err != nil {
						return err
					}
				}
				continue
			}
			// This is the existing implicit row-backed index lane, not a repair
			// for a missing required insertion output.
			member, err = preparation.PredicateMatches(evaluation, c.Parent, d, row.Values, &o, nil)
			if err != nil {
				return err
			}
			if member {
				if err := preparation.PrepareKeys(evaluation, c.Parent, d, &o, nil); err != nil {
					return err
				}
				if keys == nil {
					keys = make([]sqltype.Value, len(d.Keys))
				}
				evaluation.Row = row.Values
				if err := o.FillKeys(row.Values, keys, evaluation); err != nil {
					return err
				}
				actual = keys
			}
		}
		if member && !c.keyShape(actual) {
			return c.corrupt("item order invariant violated")
		}
		if options.HeapAllIndexed {
			// An implicit key occupies the shared scratch; preserve it only
			// when independently reevaluating its represented membership.
			if produced {
				if err := checkHeap(row, actual, member); err != nil {
					return err
				}
			}
		}
		if !options.CheckUnique || !d.Unique || !member || amcheckNullKey(actual) {
			continue
		}
		entry := storage.IndexEntry{Keys: actual, Row: row.Row, Version: row.Version}
		lo, hi := 0, len(entries)
		for lo < hi {
			mid := lo + (hi-lo)/2
			cmp, err := o.CompareKeys(ctx, entries[mid], entry)
			if err != nil {
				return err
			}
			if cmp < 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		for at := lo; at < len(entries); at++ {
			cmp, err := o.CompareKeys(ctx, entries[at], entry)
			if err != nil {
				return err
			}
			if cmp != 0 {
				break
			}
			if entries[at].Row != row.Row && live(c.Store.Versions[entries[at].Position]) {
				return c.corrupt("index uniqueness is violated")
			}
		}
		if !produced {
			entry.Keys = append([]sqltype.Value(nil), actual...)
		}
		overlay = append(overlay, entry)
	}
	if len(overlay) > 1 {
		if err := o.Sort(ctx, overlay); err != nil {
			return err
		}
		for i := 1; i < len(overlay); i++ {
			cmp, err := o.CompareKeys(ctx, overlay[i-1], overlay[i])
			if err != nil {
				return err
			}
			if cmp == 0 && overlay[i-1].Row != overlay[i].Row {
				return c.corrupt("index uniqueness is violated")
			}
		}
	}
	return ctx.Err()
}

func amcheckNullKey(keys []sqltype.Value) bool {
	for _, key := range keys {
		if key.IsNull() {
			return true
		}
	}
	return false
}
func amcheckSameKeys(a, b []sqltype.Value) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sqltype.SameDatum(a[i], b[i]) {
			return false
		}
	}
	return true
}
func (c *AmcheckCapture) checkMaps(ctx context.Context, positions map[ids.VersionID]int) error {
	if c.Root.Membership != nil {
		if len(c.Root.Membership) != len(c.Root.Entries) {
			return c.corrupt("item order invariant violated")
		}
		for row, version := range c.Root.Membership {
			if err := ctx.Err(); err != nil {
				return err
			}
			at, ok := positions[version]
			if !ok || c.Root.Entries[at].Row != row {
				return c.corrupt("item order invariant violated")
			}
		}
	}
	if c.Root.RowEntries != nil {
		if len(c.Root.RowEntries) != len(c.Root.Entries) {
			return c.corrupt("item order invariant violated")
		}
		for row, at := range c.Root.RowEntries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if at < 0 || at >= len(c.Root.Entries) || c.Root.Entries[at].Row != row {
				return c.corrupt("item order invariant violated")
			}
		}
	}
	return nil
}
func (c *AmcheckCapture) checkDescent(ctx context.Context, compare func(context.Context, storage.IndexEntry, storage.IndexEntry) (int, error)) error {
	for i, entry := range c.Root.Entries {
		found, err := amcheckSearch(ctx, c.Root.Entries, entry, compare)
		if err != nil {
			return err
		}
		if found != i {
			return amcheckError("XX002", fmt.Sprintf("could not find tuple using search from root page in index \"%s\"", c.Index.Name()))
		}
	}
	return ctx.Err()
}
func amcheckSearch(ctx context.Context, entries []storage.IndexEntry, target storage.IndexEntry, compare func(context.Context, storage.IndexEntry, storage.IndexEntry) (int, error)) (int, error) {
	lo, hi := 0, len(entries)
	for lo < hi {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		mid := lo + (hi-lo)/2
		cmp, err := compare(ctx, entries[mid], target)
		if err != nil {
			return -1, err
		}
		if cmp < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(entries) {
		cmp, err := compare(ctx, entries[lo], target)
		if err != nil {
			return -1, err
		}
		if cmp == 0 && entries[lo].Row == target.Row && entries[lo].Version == target.Version && amcheckSameKeys(entries[lo].Keys, target.Keys) {
			return lo, nil
		}
	}
	return -1, ctx.Err()
}

func (c *AmcheckCapture) keyShape(keys []sqltype.Value) bool {
	if len(keys) != len(c.Index.SQLIndex.Keys) || len(c.Index.Columns) < len(keys) {
		return false
	}
	for i, key := range keys {
		if key.Type() != c.Index.Columns[i].Usage.Type {
			return false
		}
	}
	return true
}
func (c *AmcheckCapture) rowShape(row storage.RowVersion) bool {
	if len(row.Values) != len(c.Parent.Columns) {
		return false
	}
	for i, value := range row.Values {
		if value.Type() != c.Parent.Columns[i].Usage.Type {
			return false
		}
	}
	return true
}
