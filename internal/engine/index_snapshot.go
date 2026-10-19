package engine

import (
	"context"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Captured under the source catalog/storage mutex. Replacement owns these
// containers; roots, descriptors and scalar payloads remain immutable source data.
type indexSnapshot struct {
	sources       map[ids.ObjectAddress]indexSnapshotSource
	mapping       map[ids.ObjectAddress]ids.ObjectAddress
	imports       map[*sqltype.Descriptor]*sqltype.Descriptor
	partitionRows map[ids.ObjectAddress]partitionRowHistory
}

type indexSnapshotSource struct {
	index, parent *catalog.Relation
	store         storage.Relation
	root          *storage.IndexRoot
	frontier      ids.CommitSeq
}

// captureIndexSnapshot captures the indexes of catalog, which e must own.
func (e *Engine) captureIndexSnapshot(ctx context.Context, catalog catalog.Read, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, frontier ids.CommitSeq) (*indexSnapshot, error) {
	return e.captureIndexSnapshotParents(ctx, catalog, relations, roots, frontier, nil, nil)
}

func (e *Engine) captureIndexSnapshotParents(ctx context.Context, view catalog.Read, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, frontier ids.CommitSeq, parents map[ids.ObjectAddress]*storage.Relation, private *catalogChanges) (*indexSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if view.Owner() == nil || view.Owner() != e.catalog.owner() || view.State() == nil || e.catalog.state.Epoch() != view.Epoch() || frontier != e.visibility.CommitSeq() {
		return nil, ids.ErrCatalogReplaced
	}
	authority := e.catalog.state
	if private != nil {
		authority = e.privateCatalogLocked(private).State()
	}
	actualRoot := func(address ids.ObjectAddress) *storage.IndexRoot {
		if private != nil {
			if root := private.uniqueRoots[address]; root != nil {
				return root
			}
		}
		return e.storage.IndexRoots[address]
	}
	snapshot := &indexSnapshot{sources: make(map[ids.ObjectAddress]indexSnapshotSource)}
	for address, descriptor := range view.Descriptors() {
		if parents != nil {
			index, ok := descriptor.(*catalog.Relation)
			if !ok || index.SQLIndex == nil || parents[index.SQLIndex.Parent] == nil {
				continue
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if relation, ok := descriptor.(*catalog.Relation); ok && partitionNeedsRowHistory(view, relation) {
			store := relations[address]
			if store == nil || store != e.privateStorageLocked(private, address) {
				return nil, ids.ErrCatalogReplaced
			}
			if snapshot.partitionRows == nil {
				snapshot.partitionRows = make(map[ids.ObjectAddress]partitionRowHistory)
			}
			snapshot.partitionRows[address] = partitionRowHistory{relation: relation, store: *store, frontier: frontier}
		}
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || !index.SQLIndex.Ready || !index.SQLIndex.Live {
			continue
		}
		parent, ok := view.Relation(index.SQLIndex.Parent)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		actual, exists := authority.Relation(address)
		actualParent, parentExists := authority.Relation(parent.Address())
		if !exists || !parentExists || !sameIndexKeySemantics(actual, index) || !indexLayoutInputsUnchanged(index, actualParent, parent) {
			return nil, ids.ErrCatalogReplaced
		}
		if parent.Partition != nil {
			continue
		}
		store, root := relations[parent.Address()], roots[address]
		if store == nil || root == nil || store != e.privateStorageLocked(private, parent.Address()) || root != actualRoot(address) {
			return nil, ids.ErrCatalogReplaced
		}
		if (root.Hash != nil) != (index.SQLIndex.Method == catalog.HashMethod) {
			return nil, ids.ErrCatalogReplaced
		}
		if root.Hash != nil && (root.Hash.Storage != store.IndexIdentity() || root.Hash.Generation != store.Generation || root.Hash.Ends != store.Ends || root.Hash.Prefix != len(store.Versions)) {
			return nil, ids.ErrCatalogReplaced
		}
		check := func(row ids.RowID, version ids.VersionID, position int) error {
			if position < 0 || position >= len(store.Versions) {
				return ids.ErrCatalogReplaced
			}
			actual := store.Versions[position]
			if actual.Row != row || actual.Version != version {
				return ids.ErrCatalogReplaced
			}
			return nil
		}
		if root.Hash != nil {
			for _, bucket := range root.Hash.Buckets {
				for _, entry := range bucket {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if err := check(entry.Row, entry.Version, entry.Position); err != nil {
						return nil, err
					}
				}
			}
		} else {
			for _, entry := range root.Entries {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if len(entry.Keys) != len(index.SQLIndex.Keys) {
					return nil, ids.ErrCatalogReplaced
				}
				if err := check(entry.Row, entry.Version, entry.Position); err != nil {
					return nil, err
				}
			}
		}
		snapshot.sources[address] = indexSnapshotSource{index: index, parent: parent, store: *store, root: root, frontier: frontier}
	}
	return snapshot, nil
}

func (snapshot *indexSnapshot) captureMapping(mapping map[ids.ObjectAddress]ids.ObjectAddress, imports map[*sqltype.Descriptor]*sqltype.Descriptor) {
	if snapshot == nil {
		return
	}
	snapshot.mapping, snapshot.imports = maps.Clone(mapping), maps.Clone(imports)
	if len(mapping) != 0 && len(snapshot.partitionRows) != 0 {
		histories := make(map[ids.ObjectAddress]partitionRowHistory, len(snapshot.partitionRows))
		for address, history := range snapshot.partitionRows {
			histories[snapshot.destination(address)] = history
		}
		snapshot.partitionRows = histories
	}
}

func (snapshot *indexSnapshot) destination(address ids.ObjectAddress) ids.ObjectAddress {
	if destination, ok := snapshot.mapping[address]; ok {
		return destination
	}
	return address
}

func (snapshot *indexSnapshot) datum(value sqltype.Value, target *sqltype.Descriptor, collation collationRemapper, aclImport func(sqltype.Value) (sqltype.Value, error)) (sqltype.Value, error) {
	value, err := reownDomainDatum(value, target, collation, snapshot.imports, aclImport)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !value.IsNull() && target.ContainsRegclass() {
		value, err = remapRegclassDatum(value, func(id ids.OID) ids.OID {
			return snapshot.destination(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}).ObjectID
		})
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if !value.IsNull() && (target.ContainsRegtype() || target.ContainsRegObject()) {
		value, err = remapCatalogAliasDatum(value, snapshot.destination)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if !value.IsNull() && target.ContainsRegproc() {
		value, err = remapRoutineDatum(value, func(id ids.OID) ids.OID {
			return snapshot.destination(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}).ObjectID
		})
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if !value.IsNull() && target.ContainsOperatorAlias() {
		value, err = remapOperatorDatum(value, func(id ids.OID) ids.OID {
			return snapshot.destination(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}).ObjectID
		})
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if !value.IsNull() && target.ContainsRegTS() {
		value, err = remapRegTSDatum(value, snapshot.destination)
	}
	return value, err
}

// Row identities in positions are the source identities saved before remint.
// No predicate, expression or domain conversion is executed by this transport.
func (snapshot *indexSnapshot) transport(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, positions map[ids.ObjectAddress]map[indexRowIdentity]int) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	return snapshot.transportRows(ctx, next, relations, positions, false)
}

// Same-logical-ID domain generation changes retain every physical version and
// produced key. Replacement's ordinary transport still compacts live rows.
func (snapshot *indexSnapshot) transportRows(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, positions map[ids.ObjectAddress]map[indexRowIdentity]int, preserveVersions bool) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	roots := make(map[ids.ObjectAddress]*storage.IndexRoot, len(snapshot.sources))
	checkedParents := make(map[ids.ObjectAddress]bool)
	collation := remappedCollationCapabilities(next, snapshot.mapping)
	for sourceAddress, source := range snapshot.sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		address := snapshot.destination(sourceAddress)
		index, ok := next.Relation(address)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != snapshot.destination(source.parent.Address()) || len(index.SQLIndex.Keys) != len(source.index.SQLIndex.Keys) || index.SQLIndex.Method != source.index.SQLIndex.Method {
			return nil, ids.ErrCatalogReplaced
		}
		parent, ok := next.Relation(index.SQLIndex.Parent)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		store, selected := relations[parent.Address()], positions[parent.Address()]
		if store == nil || selected == nil {
			return nil, ids.ErrCatalogReplaced
		}
		if !checkedParents[parent.Address()] {
			count := 0
			for _, row := range source.store.Versions {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if !preserveVersions && !visibility.LiveVersion(row, source.frontier, source.store.Ends, nil) {
					continue
				}
				position, ok := selected[indexRowIdentity{row: row.Row, version: row.Version}]
				if !ok || position < 0 || position >= len(store.Versions) {
					return nil, ids.ErrCatalogReplaced
				}
				count++
			}
			if count != len(selected) || count != len(store.Versions) {
				return nil, ids.ErrCatalogReplaced
			}
			checkedParents[parent.Address()] = true
		}
		usage := make([]sqltype.Usage, len(index.SQLIndex.Keys))
		for i, key := range index.SQLIndex.Keys {
			var err error
			usage[i], err = indexKeyUsage(parent, key)
			if err != nil {
				return nil, err
			}
		}
		if source.root.Hash != nil {
			binding, err := bindHashIndex(parent, index.SQLIndex)
			if err != nil {
				return nil, err
			}
			hash := &storage.HashRoot{Buckets: make(map[uint64][]storage.HashEntry), Binding: binding, Storage: store.IndexIdentity(), Generation: store.Generation, Prefix: len(store.Versions), Ends: store.Ends}
			for _, bucket := range source.root.Hash.Buckets {
				for _, entry := range bucket {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					position, ok := selected[indexRowIdentity{row: entry.Row, version: entry.Version}]
					if !ok {
						continue
					}
					entry.Key, err = snapshot.datum(entry.Key, usage[0].Type, collation, next.ACLItemImport())
					if err != nil {
						return nil, err
					}
					row := store.Versions[position]
					entry.Row, entry.Version, entry.Position = row.Row, row.Version, position
					h, err := binding.Provider.Hash(ctx, entry.Key)
					if err != nil {
						return nil, err
					}
					hash.Buckets[h] = append(hash.Buckets[h], entry)
				}
			}
			if err := hash.CaptureRowEntries(ctx, index.SQLIndex); err != nil {
				return nil, err
			}
			roots[address] = &storage.IndexRoot{Hash: hash}
		} else {
			ordering, err := bindIndexKeys(parent, index.SQLIndex)
			if err != nil {
				return nil, err
			}
			entries := make([]storage.IndexEntry, 0, min(len(source.root.Entries), len(store.Versions)))
			for _, entry := range source.root.Entries {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				position, ok := selected[indexRowIdentity{row: entry.Row, version: entry.Version}]
				if !ok {
					continue
				}
				keys := entry.Keys
				copied := false
				for i, value := range entry.Keys {
					rebound, err := snapshot.datum(value, usage[i].Type, collation, next.ACLItemImport())
					if err != nil {
						return nil, err
					}
					if rebound != value {
						if !copied {
							keys = slices.Clone(entry.Keys)
							copied = true
						}
						keys[i] = rebound
					}
				}
				row := store.Versions[position]
				entries = append(entries, storage.IndexEntry{Keys: keys, Row: row.Row, Version: row.Version, Position: position})
			}
			if index.SQLIndex.Method == catalog.BtreeMethod {
				if err := ordering.sort(ctx, entries); err != nil {
					return nil, err
				}
			}
			roots[address] = newLogicalIndexRoot(index.SQLIndex, entries)
		}
	}
	return roots, ctx.Err()
}
