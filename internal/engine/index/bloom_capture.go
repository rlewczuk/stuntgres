package index

import (
	"cmp"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
)

// BloomRootCompatible checks captured operation metadata without evaluating a
// datum. A stale provider or key program is a scan fallback, never probe authority.
// Capture and EXPLAIN use the same check against their already admitted catalog.
func BloomRootCompatible(view catalog.Read, parent, index *catalog.Relation, carrier *storage.IndexRoot) bool {
	if carrier == nil || carrier.Bloom == nil || index.SQLIndex == nil {
		return false
	}
	definition, root := index.SQLIndex, carrier.Bloom
	if definition.Method != catalog.BloomMethod || !definition.Ready || !definition.Valid || !definition.Live || definition.Predicate != nil || root.Options != BloomOptions(index) || len(root.Bindings) != len(definition.Keys) || len(root.Entries) != len(carrier.Entries) {
		return false
	}
	for i, key := range definition.Keys {
		usage, err := KeyUsage(parent, key)
		if err != nil || usage != root.Bindings[i].Usage {
			return false
		}
		class, err := routine.BindClassSupport(view, key.Class)
		if err != nil || !class.SameIdentity(root.Bindings[i].Class) || class.Support(1) != root.Bindings[i].Class.Support(1) {
			return false
		}
		slot := -1
		if key.Derived == nil {
			slot = slices.Index(parent.Attributes, key.Attribute)
		}
		if slot != root.Bindings[i].Slot {
			return false
		}
	}
	return true
}

// CaptureBloomIndexesLocked shares the committed source frontier and mutex
// authority with hash capture. A failed coverage proof always leaves a scan.
func CaptureBloomIndexesLocked(in HashCapture, leaf exec.Leaf, source *exec.Source, mark plan.RowMark) error {
	if !in.Eligible || mark.Mode != 0 || source == nil || source.View == nil || source.Inheritance != nil || len(source.Own) != 0 || source.RLS != nil || source.RlsDenied {
		return nil
	}
	data := source.View.Data()
	if data.Catalog.State() != in.Live {
		return nil
	}
	address := leaf.Ref.Address
	store := data.GenerationAt(address)
	if store == nil || store != in.Relations[address] || data.FrontierAt(address) != in.Commit || data.Deleted[address] != nil || len(source.Committed) != len(store.Versions) || data.Ends[address] != store.Ends {
		return nil
	}
	for indexAddress, carrier := range in.Roots {
		index, ok := data.Catalog.Relation(indexAddress)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != address || !BloomRootCompatible(data.Catalog, leaf.Relation, index, carrier) {
			continue
		}
		live, ok := in.Live.Relation(indexAddress)
		if !ok || !SameIndexKeySemantics(index, live) {
			continue
		}
		root := carrier.Bloom
		if root.Storage != store || root.Generation != store.Generation || root.Prefix != len(source.Committed) || root.Ends != data.Ends[address] {
			continue
		}
		if in.Factory != nil {
			method, ok := data.Catalog.AccessMethod(index.SQLIndex.MethodIdentity())
			if !ok {
				return ids.ErrCatalogReplaced
			}
			physical, err := in.Factory.InitializeCapturedIndexMethodLocked(data.Catalog, method)
			if err != nil {
				return err
			}
			if physical != catalog.BloomMethod {
				return ids.ErrCatalogReplaced
			}
		} else if owner := data.Catalog.Owner(); owner != nil && owner.Sepgsql != nil {
			return fmt.Errorf("stuntgres: missing captured index method factory")
		}
		source.BloomIndexes = append(source.BloomIndexes, exec.CapturedBloomIndex{
			Address: indexAddress, Name: index.Name(), Definition: index.SQLIndex, Root: carrier,
		})
	}
	slices.SortFunc(source.BloomIndexes, func(a, b exec.CapturedBloomIndex) int {
		return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID)
	})
	return nil
}
