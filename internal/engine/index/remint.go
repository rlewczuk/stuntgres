package index

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// RemintBase is the engine state RemintStorage reads. The caller captures it
// under Engine.mu and holds the lock until RemintStorage returns.
type RemintBase struct {
	Catalog              *catalog.State // The published catalog.
	Relations            map[ids.ObjectAddress]*storage.Relation
	Roots                map[ids.ObjectAddress]*storage.IndexRoot
	NextRow, NextVersion uint64 // The row and version allocator frontiers.
	Frontier             ids.CommitSeq
	PreserveProvenance   bool // Self replacement stays in the same XID universe.
}

// RemintStorage gives the imported storage of next fresh row and version
// identities, transports the snapshot's index roots onto them and validates
// the unique and exclusion roots. It returns the roots and the advanced
// allocator frontiers.
//
// Imported storage is private to replacement. Exact retained TEMP storage keeps
// its internal lifetime and row identities. Allocator frontiers advance only
// after comparison, root construction and cancellation have all succeeded.
func RemintStorage(ctx context.Context, view catalog.Read, base RemintBase, relations map[ids.ObjectAddress]*storage.Relation, snapshot *Snapshot) (map[ids.ObjectAddress]*storage.IndexRoot, uint64, uint64, error) {
	next := view.State()
	retained := catalog.NewState(0)
	for address, descriptor := range next.Descriptors() {
		namespace, temporary := next.Namespace(descriptor.Namespace())
		if temporary && namespace.Temporary != (ids.SessionToken{}) && descriptor == base.Catalog.Get(address) {
			retained.SetDescriptor(address, descriptor)
			continue
		}
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil {
			continue
		}
		if !idx.SQLIndex.Ready || !idx.SQLIndex.Valid || !idx.SQLIndex.Live {
			parent, exists := next.Relation(idx.SQLIndex.Parent)
			if exists && parent.Partition != nil && idx.SQLIndex.Ready && idx.SQLIndex.Live {
				continue
			}
			return nil, 0, 0, fmt.Errorf("stuntgres: replacement cannot import invalid index %s", catalog.QuoteIdentifier(idx.Name()))
		}
		if idx.SQLIndex.Method != catalog.GinMethod {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, 0, 0, err
		}
		parent, exists := next.Relation(idx.SQLIndex.Parent)
		if !exists {
			return nil, 0, 0, ids.ErrCatalogReplaced
		}
		bindings, err := BindGinIndex(view, parent, idx.SQLIndex)
		if err != nil {
			return nil, 0, 0, err
		}
		// Symbolic import deliberately drops source support seals. Capture the
		// destination's actual providers in its private descriptor before root
		// transport captures that descriptor and its construction history.
		var keys []catalog.IndexKey
		for i, binding := range bindings {
			if idx.SQLIndex.Keys[i].Class != binding.Class {
				if keys == nil {
					keys = slices.Clone(idx.SQLIndex.Keys)
				}
				keys[i].Class = binding.Class
			}
		}
		if keys != nil {
			current, definition := *idx, *idx.SQLIndex
			definition.Keys = keys
			current.SQLIndex = &definition
			next.SetDescriptor(address, &current)
		}
	}
	rowID, versionID := base.NextRow, base.NextVersion
	if snapshot == nil {
		return nil, 0, 0, ids.ErrCatalogReplaced
	}
	positions := make(map[ids.ObjectAddress]map[RowIdentity]int, len(relations))
	for address, relation := range relations {
		if descriptor := next.Get(address); descriptor != nil && base.Relations[address] == relation {
			if namespace, ok := next.Namespace(descriptor.Namespace()); ok && namespace.Temporary != (ids.SessionToken{}) {
				continue
			}
		}
		selected := make(map[RowIdentity]int, len(relation.Versions))
		positions[address] = selected
		for i := range relation.Versions {
			rowID++
			versionID++
			row := &relation.Versions[i]
			if err := ctx.Err(); err != nil {
				return nil, 0, 0, err
			}
			identity := RowIdentity{Row: row.Row, Version: row.Version}
			if _, duplicate := selected[identity]; duplicate {
				return nil, 0, 0, ids.ErrCatalogReplaced
			}
			selected[identity] = i
			row.Row, row.Version = ids.RowID(rowID), ids.VersionID(versionID)
			row.Base, row.Born = 0, base.Frontier+1
			row.DeleteFullXID, row.ForcedFrozen, row.RepairStamp, row.ForcedEnd = 0, false, 0, 0
			row.HeapKilled = false
			if !base.PreserveProvenance {
				row.InsertFullXID, row.Frozen, row.ImportFrozen = 0, true, true
			}
			row.KeyChangedAt, row.PendingKeyChange = 0, false
			row.IndexAdmission = nil
			row.IndexOutputs = nil
			row.PrivateHistory = nil
		}
		// Capture/remint contains only committed visible rows. Current
		// cardinality survives transport; scan history and cumulative activity
		// belong to the replaced incarnation and must not be inherited.
		relation.Maintenance = storage.Maintenance{Live: int64(len(relation.Versions))}
		relation.Retained = nil
		relation.Discarded, relation.Headers, relation.Ends = nil, nil, nil
	}
	roots, err := snapshot.Transport(ctx, view, relations, positions)
	if err != nil {
		return nil, 0, 0, err
	}
	validationView := access.View{Catalog: view}
	for address, descriptor := range next.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || !idx.SQLIndex.Ready || !idx.SQLIndex.Live {
			continue
		}
		parent, ok := next.Relation(idx.SQLIndex.Parent)
		if !ok {
			return nil, 0, 0, ids.ErrCatalogReplaced
		}
		if parent.Partition != nil {
			continue
		}
		if retained.Get(address) != nil {
			root := base.Roots[address]
			if root == nil {
				return nil, 0, 0, ids.ErrCatalogReplaced
			}
			if idx.SQLIndex.Method == catalog.GinMethod {
				if base.Relations[parent.Address()] != relations[parent.Address()] {
					return nil, 0, 0, ids.ErrCatalogReplaced
				}
				current, rebound, err := rebindRetainedGin(ctx, view.WithState(base.Catalog), view, parent, idx, root)
				if err != nil {
					return nil, 0, 0, err
				}
				if current != idx {
					next.SetDescriptor(address, current)
				}
				root = rebound
			}
			roots[address] = root
			continue
		}
		root := roots[address]
		if namespace, ok := next.Namespace(parent.Namespace()); ok && namespace.Temporary.IsDetached() {
			root, err = emptyIndexRoot(ctx, view, parent, idx, relations[parent.Address()])
			if err != nil {
				return nil, 0, 0, err
			}
			roots[address] = root
		}
		if root == nil {
			return nil, 0, 0, ids.ErrCatalogReplaced
		}
		schema := ""
		if idx.SQLIndex.Unique || idx.SQLIndex.IsExclusion() {
			namespace := next.Get(parent.Namespace())
			if namespace == nil {
				return nil, 0, 0, ids.ErrCatalogReplaced
			}
			schema = namespace.Name()
		}
		if err := ValidateUniqueRoot(ctx, parent, idx, root, schema, sqltype.OutputEnvironment{}, &validationView, nil, true); err != nil {
			return nil, 0, 0, err
		}
	}
	return roots, rowID, versionID, ctx.Err()
}
