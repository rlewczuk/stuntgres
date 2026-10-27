package index

import (
	"cmp"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
)

// HashCapture is the committed engine state a query source's hash index
// capture is checked against. The engine fills it under Engine.mu.
type HashCapture struct {
	Eligible  bool                                    // An implicit statement without a private catalog.
	Live      *catalog.State                          // The published catalog.
	Relations map[ids.ObjectAddress]*storage.Relation // Published row storage.
	Roots     map[ids.ObjectAddress]*storage.IndexRoot
	Commit    ids.CommitSeq // The current commit frontier.
}

// CaptureHashIndexesLocked appends to source the published hash index roots
// that exactly cover a plain table source's committed prefix. A source that
// does not match the published state captures none. After this proof,
// probing consults only the retained root and retained source rows. The
// caller holds Engine.mu.
func CaptureHashIndexesLocked(in HashCapture, leaf exec.Leaf, source *exec.Source, mark plan.RowMark) error {
	if !in.Eligible || mark.Mode != 0 || source.View == nil || source.Inheritance != nil || len(source.Own) != 0 {
		return nil
	}
	data := source.View.Data()
	if data.Catalog.State() != in.Live {
		return nil
	}
	a := leaf.Ref.Address
	store := data.GenerationAt(a)
	if store == nil || store != in.Relations[a] || data.FrontierAt(a) != in.Commit || data.Deleted[a] != nil || len(source.Committed) != len(store.Versions) || data.Ends[a] != store.Ends {
		return nil
	}
	for address, carrier := range in.Roots {
		idx, ok := data.Catalog.Relation(address)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Method != catalog.HashMethod || idx.SQLIndex.Parent != a || carrier == nil || carrier.Hash == nil {
			continue
		}
		// Readiness permits maintenance, not complete query coverage. A
		// grandfathered commit may legitimately be absent from an invalid root.
		if !idx.SQLIndex.Ready || !idx.SQLIndex.Valid || !idx.SQLIndex.Live {
			continue
		}
		live, ok := in.Live.Relation(address)
		if !ok || !SameIndexKeySemantics(idx, live) {
			continue
		}
		root := carrier.Hash
		if root.Storage != store || root.Generation != store.Generation || root.Prefix != len(source.Committed) || root.Ends != data.Ends[a] {
			continue
		}
		usage, err := KeyUsage(leaf.Relation, idx.SQLIndex.Keys[0])
		if err != nil {
			return err
		}
		if usage != root.Binding.Usage {
			continue
		}
		source.HashIndexes = append(source.HashIndexes, exec.CapturedHashIndex{Address: address, Name: idx.Name(), Definition: idx.SQLIndex, Root: root, Usage: usage})
	}
	slices.SortFunc(source.HashIndexes, func(a, b exec.CapturedHashIndex) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	return nil
}
