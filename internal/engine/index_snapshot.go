package engine

import (
	"context"
	"maps"
	"slices"
	"stuntgres/internal/engine/ids"
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
	index, parent *relationDescriptor
	store         relationStorage
	root          *logicalIndexRoot
	frontier      CommitSeq
}

func captureIndexSnapshot(ctx context.Context, catalog CatalogRead, storage map[ids.ObjectAddress]*relationStorage, roots map[ids.ObjectAddress]*logicalIndexRoot, frontier CommitSeq) (*indexSnapshot, error) {
	return captureIndexSnapshotParents(ctx, catalog, storage, roots, frontier, nil, nil)
}

func captureIndexSnapshotParents(ctx context.Context, catalog CatalogRead, storage map[ids.ObjectAddress]*relationStorage, roots map[ids.ObjectAddress]*logicalIndexRoot, frontier CommitSeq, parents map[ids.ObjectAddress]*relationStorage, private *catalogChanges) (*indexSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if catalog.owner == nil || catalog.state == nil || catalog.owner.state.epoch != catalog.Epoch() || frontier != catalog.owner.commitSeq {
		return nil, ErrCatalogReplaced
	}
	authority := catalog.owner.state
	if private != nil {
		authority = catalog.owner.privateCatalogLocked(private).state
	}
	actualRoot := func(address ids.ObjectAddress) *logicalIndexRoot {
		if private != nil {
			if root := private.uniqueRoots[address]; root != nil {
				return root
			}
		}
		return catalog.owner.indexRoots[address]
	}
	snapshot := &indexSnapshot{sources: make(map[ids.ObjectAddress]indexSnapshotSource)}
	for address, descriptor := range catalog.state.descriptors {
		if parents != nil {
			index, ok := descriptor.(*relationDescriptor)
			if !ok || index.sqlIndex == nil || parents[index.sqlIndex.parent] == nil {
				continue
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if relation, ok := descriptor.(*relationDescriptor); ok && partitionNeedsRowHistory(catalog, relation) {
			store := storage[address]
			if store == nil || store != catalog.owner.privateStorageLocked(private, address) {
				return nil, ErrCatalogReplaced
			}
			if snapshot.partitionRows == nil {
				snapshot.partitionRows = make(map[ids.ObjectAddress]partitionRowHistory)
			}
			snapshot.partitionRows[address] = partitionRowHistory{relation: relation, store: *store, frontier: frontier}
		}
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || !index.sqlIndex.ready || !index.sqlIndex.live {
			continue
		}
		parent, ok := catalog.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		if !ok {
			return nil, ErrCatalogReplaced
		}
		actual, exists := authority.descriptors[address].(*relationDescriptor)
		actualParent, parentExists := authority.descriptors[parent.address].(*relationDescriptor)
		if !exists || !parentExists || !sameIndexKeySemantics(actual, index) || !indexLayoutInputsUnchanged(index, actualParent, parent) {
			return nil, ErrCatalogReplaced
		}
		if parent.partition != nil {
			continue
		}
		store, root := storage[parent.address], roots[address]
		if store == nil || root == nil || store != catalog.owner.privateStorageLocked(private, parent.address) || root != actualRoot(address) {
			return nil, ErrCatalogReplaced
		}
		if (root.hash != nil) != (index.sqlIndex.method == hashMethod) {
			return nil, ErrCatalogReplaced
		}
		if root.hash != nil && (root.hash.storage != indexStorageIdentity(store) || root.hash.generation != store.generation || root.hash.ends != store.ends || root.hash.prefix != len(store.versions)) {
			return nil, ErrCatalogReplaced
		}
		check := func(row RowID, version VersionID, position int) error {
			if position < 0 || position >= len(store.versions) {
				return ErrCatalogReplaced
			}
			actual := store.versions[position]
			if actual.row != row || actual.version != version {
				return ErrCatalogReplaced
			}
			return nil
		}
		if root.hash != nil {
			for _, bucket := range root.hash.buckets {
				for _, entry := range bucket {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if err := check(entry.row, entry.version, entry.position); err != nil {
						return nil, err
					}
				}
			}
		} else {
			for _, entry := range root.entries {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if len(entry.keys) != len(index.sqlIndex.keys) {
					return nil, ErrCatalogReplaced
				}
				if err := check(entry.row, entry.version, entry.position); err != nil {
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
func (snapshot *indexSnapshot) transport(ctx context.Context, next *catalogState, storage map[ids.ObjectAddress]*relationStorage, positions map[ids.ObjectAddress]map[indexRowIdentity]int) (map[ids.ObjectAddress]*logicalIndexRoot, error) {
	return snapshot.transportRows(ctx, next, storage, positions, false)
}

// Same-logical-ID domain generation changes retain every physical version and
// produced key. Replacement's ordinary transport still compacts live rows.
func (snapshot *indexSnapshot) transportRows(ctx context.Context, next *catalogState, storage map[ids.ObjectAddress]*relationStorage, positions map[ids.ObjectAddress]map[indexRowIdentity]int, preserveVersions bool) (map[ids.ObjectAddress]*logicalIndexRoot, error) {
	roots := make(map[ids.ObjectAddress]*logicalIndexRoot, len(snapshot.sources))
	checkedParents := make(map[ids.ObjectAddress]bool)
	collation := remappedCollationCapabilities(next, snapshot.mapping)
	for sourceAddress, source := range snapshot.sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		address := snapshot.destination(sourceAddress)
		index, ok := next.descriptors[address].(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.parent != snapshot.destination(source.parent.address) || len(index.sqlIndex.keys) != len(source.index.sqlIndex.keys) || index.sqlIndex.method != source.index.sqlIndex.method {
			return nil, ErrCatalogReplaced
		}
		parent, ok := next.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		if !ok {
			return nil, ErrCatalogReplaced
		}
		store, selected := storage[parent.address], positions[parent.address]
		if store == nil || selected == nil {
			return nil, ErrCatalogReplaced
		}
		if !checkedParents[parent.address] {
			count := 0
			for _, row := range source.store.versions {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if !preserveVersions && !liveVersion(row, source.frontier, source.store.ends, nil) {
					continue
				}
				position, ok := selected[indexRowIdentity{row: row.row, version: row.version}]
				if !ok || position < 0 || position >= len(store.versions) {
					return nil, ErrCatalogReplaced
				}
				count++
			}
			if count != len(selected) || count != len(store.versions) {
				return nil, ErrCatalogReplaced
			}
			checkedParents[parent.address] = true
		}
		usage := make([]sqltype.Usage, len(index.sqlIndex.keys))
		for i, key := range index.sqlIndex.keys {
			var err error
			usage[i], err = indexKeyUsage(parent, key)
			if err != nil {
				return nil, err
			}
		}
		if source.root.hash != nil {
			binding, err := bindHashIndex(parent, index.sqlIndex)
			if err != nil {
				return nil, err
			}
			hash := &hashIndexRoot{buckets: make(map[uint64][]hashIndexEntry), binding: binding, storage: indexStorageIdentity(store), generation: store.generation, prefix: len(store.versions), ends: store.ends}
			for _, bucket := range source.root.hash.buckets {
				for _, entry := range bucket {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					position, ok := selected[indexRowIdentity{row: entry.row, version: entry.version}]
					if !ok {
						continue
					}
					entry.key, err = snapshot.datum(entry.key, usage[0].Type, collation, next.aclItemImport)
					if err != nil {
						return nil, err
					}
					row := store.versions[position]
					entry.row, entry.version, entry.position = row.row, row.version, position
					h, err := binding.provider.Hash(ctx, entry.key)
					if err != nil {
						return nil, err
					}
					hash.buckets[h] = append(hash.buckets[h], entry)
				}
			}
			if err := hash.captureRowEntries(ctx, index.sqlIndex); err != nil {
				return nil, err
			}
			roots[address] = &logicalIndexRoot{hash: hash}
		} else {
			ordering, err := bindIndexKeys(parent, index.sqlIndex)
			if err != nil {
				return nil, err
			}
			entries := make([]logicalIndexEntry, 0, min(len(source.root.entries), len(store.versions)))
			for _, entry := range source.root.entries {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				position, ok := selected[indexRowIdentity{row: entry.row, version: entry.version}]
				if !ok {
					continue
				}
				keys := entry.keys
				copied := false
				for i, value := range entry.keys {
					rebound, err := snapshot.datum(value, usage[i].Type, collation, next.aclItemImport)
					if err != nil {
						return nil, err
					}
					if rebound != value {
						if !copied {
							keys = slices.Clone(entry.keys)
							copied = true
						}
						keys[i] = rebound
					}
				}
				row := store.versions[position]
				entries = append(entries, logicalIndexEntry{keys: keys, row: row.row, version: row.version, position: position})
			}
			if index.sqlIndex.method == btreeMethod {
				if err := ordering.sort(ctx, entries); err != nil {
					return nil, err
				}
			}
			roots[address] = newLogicalIndexRoot(index.sqlIndex, entries)
		}
	}
	return roots, ctx.Err()
}
