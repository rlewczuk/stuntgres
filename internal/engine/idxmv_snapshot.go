package engine

import (
	"context"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Captured under the source catalog/storage mutex. Replacement owns these
// containers; roots, descriptors and scalar payloads remain immutable source data.
type indexSnapshot struct {
	Sources       map[ids.ObjectAddress]indexSnapshotSource
	Mapping       map[ids.ObjectAddress]ids.ObjectAddress
	imports       map[*sqltype.Descriptor]*sqltype.Descriptor
	PartitionRows map[ids.ObjectAddress]partitionRowHistory
}

type indexSnapshotSource struct {
	Index, Parent *catalog.Relation
	Store         storage.Relation
	Root          *storage.IndexRoot
	Frontier      ids.CommitSeq
}

func (snapshot *indexSnapshot) CaptureMapping(mapping map[ids.ObjectAddress]ids.ObjectAddress, imports map[*sqltype.Descriptor]*sqltype.Descriptor) {
	if snapshot == nil {
		return
	}
	snapshot.Mapping, snapshot.imports = maps.Clone(mapping), maps.Clone(imports)
	if len(mapping) != 0 && len(snapshot.PartitionRows) != 0 {
		histories := make(map[ids.ObjectAddress]partitionRowHistory, len(snapshot.PartitionRows))
		for address, history := range snapshot.PartitionRows {
			histories[snapshot.Destination(address)] = history
		}
		snapshot.PartitionRows = histories
	}
}

func (snapshot *indexSnapshot) Destination(address ids.ObjectAddress) ids.ObjectAddress {
	if destination, ok := snapshot.Mapping[address]; ok {
		return destination
	}
	return address
}

func (snapshot *indexSnapshot) Datum(value sqltype.Value, target *sqltype.Descriptor, collation typedef.CollationRemapper, aclImport func(sqltype.Value) (sqltype.Value, error)) (sqltype.Value, error) {
	value, err := typedef.ReownDomainDatum(value, target, collation, snapshot.imports, aclImport)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !value.IsNull() && target.ContainsRegclass() {
		value, err = regobj.RemapClassDatum(value, func(id ids.OID) ids.OID {
			return snapshot.Destination(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}).ObjectID
		})
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if !value.IsNull() && (target.ContainsRegtype() || target.ContainsRegObject()) {
		value, err = regobj.RemapAliasDatum(value, snapshot.Destination)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if !value.IsNull() && target.ContainsRegproc() {
		value, err = regobj.RemapRoutineDatum(value, func(id ids.OID) ids.OID {
			return snapshot.Destination(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}).ObjectID
		})
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if !value.IsNull() && target.ContainsOperatorAlias() {
		value, err = regobj.RemapOperatorDatum(value, func(id ids.OID) ids.OID {
			return snapshot.Destination(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}).ObjectID
		})
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if !value.IsNull() && target.ContainsRegTS() {
		value, err = regobj.RemapTextSearchDatum(value, snapshot.Destination)
	}
	return value, err
}

// Row identities in positions are the source identities saved before remint.
// No predicate, expression or domain conversion is executed by this transport.
func (snapshot *indexSnapshot) Transport(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, positions map[ids.ObjectAddress]map[indexRowIdentity]int) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	return snapshot.TransportRows(ctx, next, relations, positions, false)
}

// Same-logical-ID domain generation changes retain every physical version and
// produced key. Replacement's ordinary transport still compacts live rows.
func (snapshot *indexSnapshot) TransportRows(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, positions map[ids.ObjectAddress]map[indexRowIdentity]int, preserveVersions bool) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	roots := make(map[ids.ObjectAddress]*storage.IndexRoot, len(snapshot.Sources))
	checkedParents := make(map[ids.ObjectAddress]bool)
	collation := typedef.RemappedCollationCapabilities(next, snapshot.Mapping)
	for sourceAddress, source := range snapshot.Sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		address := snapshot.Destination(sourceAddress)
		index, ok := next.Relation(address)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != snapshot.Destination(source.Parent.Address()) || len(index.SQLIndex.Keys) != len(source.Index.SQLIndex.Keys) || index.SQLIndex.Method != source.Index.SQLIndex.Method {
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
			for _, row := range source.Store.Versions {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if !preserveVersions && !visibility.LiveVersion(row, source.Frontier, source.Store.Ends, nil) {
					continue
				}
				position, ok := selected[indexRowIdentity{Row: row.Row, Version: row.Version}]
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
			usage[i], err = KeyUsage(parent, key)
			if err != nil {
				return nil, err
			}
		}
		if source.Root.Hash != nil {
			binding, err := BindHashIndex(parent, index.SQLIndex)
			if err != nil {
				return nil, err
			}
			hash := &storage.HashRoot{Buckets: make(map[uint64][]storage.HashEntry), Binding: binding, Storage: store.IndexIdentity(), Generation: store.Generation, Prefix: len(store.Versions), Ends: store.Ends}
			for _, bucket := range source.Root.Hash.Buckets {
				for _, entry := range bucket {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					position, ok := selected[indexRowIdentity{Row: entry.Row, Version: entry.Version}]
					if !ok {
						continue
					}
					entry.Key, err = snapshot.Datum(entry.Key, usage[0].Type, collation, next.ACLItemImport())
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
			ordering, err := BindIndexKeys(parent, index.SQLIndex)
			if err != nil {
				return nil, err
			}
			entries := make([]storage.IndexEntry, 0, min(len(source.Root.Entries), len(store.Versions)))
			for _, entry := range source.Root.Entries {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				position, ok := selected[indexRowIdentity{Row: entry.Row, Version: entry.Version}]
				if !ok {
					continue
				}
				keys := entry.Keys
				copied := false
				for i, value := range entry.Keys {
					rebound, err := snapshot.Datum(value, usage[i].Type, collation, next.ACLItemImport())
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
				if err := ordering.Sort(ctx, entries); err != nil {
					return nil, err
				}
			}
			roots[address] = NewLogicalIndexRoot(index.SQLIndex, entries)
		}
	}
	return roots, ctx.Err()
}
