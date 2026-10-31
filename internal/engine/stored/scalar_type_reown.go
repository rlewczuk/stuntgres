package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// ReownScalarTypes points the column types of every relation in next, and
// the datums of relations, at the current type generations after a type
// change or a renumbering by mapping. It rebinds domain datums and remaps
// reg* and aclitem values; changed relations and descriptors are copies.
func ReownScalarTypes(next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
	collation := typedef.RemappedCollationCapabilities(next, mapping)
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if d, ok := next.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.ID())}); ok {
			return d.SQLType
		}
		return nil
	}
	for address, descriptor := range next.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if destination, ok := mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)}]; ok {
			copy := *d
			copy.RowType = sqltype.ID(destination.ObjectID)
			next.SetDescriptor(address, &copy)
			d = &copy
		}
		if destination, ok := mapping[d.OfType]; ok {
			copy := *d
			copy.OfType = destination
			next.SetDescriptor(address, &copy)
			d = &copy
		}
		var columns []catalog.Column
		for i, c := range d.Columns {
			target := typ(c.Usage.Type)
			if target == nil {
				return fmt.Errorf("stuntgres: missing imported column type")
			}
			capability := c.Usage.Collation
			if collation != nil {
				var err error
				capability, err = collation(capability)
				if err != nil {
					return err
				}
			}
			if target != c.Usage.Type || capability != c.Usage.Collation {
				if columns == nil {
					columns = append([]catalog.Column(nil), d.Columns...)
				}
				columns[i].Usage.Type = target
				columns[i].Usage.Collation = capability
			}
		}
		if columns != nil {
			copy := *d
			copy.Columns = columns
			next.SetDescriptor(address, &copy)
		}
	}
	return reownScalarRows(next, relations, mapping)
}

// Row transport reads immutable catalog generations and owns only copy-on-change
// output stores. It must not normalize descriptors in a published private view.
func reownScalarRows(next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
	collation := typedef.RemappedCollationCapabilities(next, mapping)
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if d, ok := next.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.ID())}); ok {
			return d.SQLType
		}
		return nil
	}
	header := func(row storage.RowVersion) (storage.RowVersion, bool, error) {
		var values []sqltype.Value
		for j, value := range row.Values {
			target := typ(value.Type())
			if target == nil {
				return storage.RowVersion{}, false, fmt.Errorf("stuntgres: missing imported datum type")
			}
			rebound := value
			changed := target != value.Type()
			var err error
			if changed || next.ACLItemImport() != nil && value.Type().ContainsACLItem() {
				rebound, err = typedef.ReownDomainDatum(value, target, collation, next.ScalarImports(), next.ACLItemImport())
				if err != nil {
					return storage.RowVersion{}, false, err
				}
				changed = changed || rebound != value
			}
			if len(mapping) != 0 && !rebound.IsNull() && target.ContainsRegclass() {
				remapped, er := regobj.RemapClassDatum(rebound, func(id ids.OID) ids.OID {
					if target, ok := mapping[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}]; ok {
						return target.ObjectID
					}
					return id
				})
				if er != nil {
					return storage.RowVersion{}, false, er
				}
				changed = changed || remapped != rebound
				rebound = remapped
			}
			if len(mapping) != 0 && !rebound.IsNull() && (target.ContainsRegtype() || target.ContainsRegObject()) {
				remapped, er := regobj.RemapAliasDatum(rebound, func(address ids.ObjectAddress) ids.ObjectAddress {
					if target, ok := mapping[address]; ok {
						return target
					}
					return address
				})
				if er != nil {
					return storage.RowVersion{}, false, er
				}
				changed = changed || remapped != rebound
				rebound = remapped
			}
			if len(mapping) != 0 && !rebound.IsNull() && target.ContainsRegproc() {
				remapped, er := regobj.RemapRoutineDatum(rebound, func(id ids.OID) ids.OID {
					if target, ok := mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}]; ok {
						return target.ObjectID
					}
					return id
				})
				if er != nil {
					return storage.RowVersion{}, false, er
				}
				changed = changed || remapped != rebound
				rebound = remapped
			}
			if len(mapping) != 0 && !rebound.IsNull() && target.ContainsOperatorAlias() {
				remapped, er := regobj.RemapOperatorDatum(rebound, func(id ids.OID) ids.OID {
					if target, ok := mapping[ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}]; ok {
						return target.ObjectID
					}
					return id
				})
				if er != nil {
					return storage.RowVersion{}, false, er
				}
				changed = changed || remapped != rebound
				rebound = remapped
			}
			if len(mapping) != 0 && !rebound.IsNull() && target.ContainsRegTS() {
				remapped, er := regobj.RemapTextSearchDatum(rebound, func(address ids.ObjectAddress) ids.ObjectAddress {
					if target, ok := mapping[address]; ok {
						return target
					}
					return address
				})
				if er != nil {
					return storage.RowVersion{}, false, er
				}
				changed = changed || remapped != rebound
				rebound = remapped
			}
			if changed {
				if values == nil {
					values = append([]sqltype.Value(nil), row.Values...)
				}
				values[j] = rebound
			}
		}
		if values == nil {
			return row, false, nil
		}
		row.Values, row.IndexOutputs = values, nil
		return row, true, nil
	}
	for address, relation := range relations {
		var versions []storage.RowVersion
		for i, row := range relation.Versions {
			mapped, changed, err := header(row)
			if err != nil {
				return err
			}
			history, err := storage.MapHeapPredecessors(row.PrivateHistory, header)
			if err != nil {
				return err
			}
			if changed || history != row.PrivateHistory {
				if versions == nil {
					versions = append([]storage.RowVersion(nil), relation.Versions...)
				}
				mapped.PrivateHistory = history
				versions[i] = mapped
			}
		}
		retained, err := storage.MapHeapPredecessors(relation.Retained, header)
		if err != nil {
			return err
		}
		if versions != nil || retained != relation.Retained {
			copy := *relation
			if versions != nil {
				copy.Versions = versions
			}
			copy.Retained = retained
			relations[address] = &copy
		}
	}
	return nil
}

// ReownVersions points private writes and retained owner snapshots at the
// current type generations of next, an immutable catalog snapshot. Statement
// admission protects the input slice; output values are copied only on change.
// It never mutates next or rewrites logical row identities/end roots/frontiers.
func ReownVersions(next *catalog.State, versions []storage.RowVersion) ([]storage.RowVersion, error) {
	if len(versions) == 0 {
		return versions, nil
	}
	address := ids.ObjectAddress{}
	stores := map[ids.ObjectAddress]*storage.Relation{address: {Versions: versions}}
	if err := reownScalarRows(next, stores, nil); err != nil {
		return nil, err
	}
	return stores[address].Versions, nil
}
