package index

import (
	"context"
	"fmt"

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
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil && (!index.SQLIndex.Ready || !index.SQLIndex.Valid || !index.SQLIndex.Live) {
			parent, exists := next.Relation(index.SQLIndex.Parent)
			if exists && parent.Partition != nil && index.SQLIndex.Ready && index.SQLIndex.Live {
				continue
			}
			return nil, 0, 0, fmt.Errorf("stuntgres: replacement cannot import invalid index %s", catalog.QuoteIdentifier(index.Name()))
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
			if !base.PreserveProvenance {
				row.InsertFullXID, row.Frozen, row.ImportFrozen = 0, true, true
			}
			row.KeyChangedAt, row.PendingKeyChange = 0, false
			row.IndexAdmission = nil
			row.IndexOutputs = nil
		}
		// Capture/remint contains only committed visible rows. Current
		// cardinality survives transport; scan history and cumulative activity
		// belong to the replaced incarnation and must not be inherited.
		relation.Maintenance = storage.Maintenance{Live: int64(len(relation.Versions))}
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
			roots[address] = root
			continue
		}
		root := roots[address]
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
