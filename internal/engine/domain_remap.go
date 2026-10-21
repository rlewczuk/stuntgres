package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func reownCatalogScalarTypes(next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
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
	for address, relation := range relations {
		var versions []storage.RowVersion
		for i, row := range relation.Versions {
			var values []sqltype.Value
			for j, value := range row.Values {
				target := typ(value.Type())
				if target == nil {
					return fmt.Errorf("stuntgres: missing imported datum type")
				}
				rebound := value
				changed := target != value.Type()
				var err error
				if changed || next.ACLItemImport() != nil && value.Type().ContainsACLItem() {
					rebound, err = typedef.ReownDomainDatum(value, target, collation, next.ScalarImports(), next.ACLItemImport())
					if err != nil {
						return err
					}
					changed = changed || rebound != value
				}
				if len(mapping) != 0 && !rebound.IsNull() && target.ContainsRegclass() {
					remapped, er := remapRegclassDatum(rebound, func(id ids.OID) ids.OID {
						if target, ok := mapping[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}]; ok {
							return target.ObjectID
						}
						return id
					})
					if er != nil {
						return er
					}
					changed = changed || remapped != rebound
					rebound = remapped
				}
				if len(mapping) != 0 && !rebound.IsNull() && (target.ContainsRegtype() || target.ContainsRegObject()) {
					remapped, er := remapCatalogAliasDatum(rebound, func(address ids.ObjectAddress) ids.ObjectAddress {
						if target, ok := mapping[address]; ok {
							return target
						}
						return address
					})
					if er != nil {
						return er
					}
					changed = changed || remapped != rebound
					rebound = remapped
				}
				if len(mapping) != 0 && !rebound.IsNull() && target.ContainsRegproc() {
					remapped, er := remapRoutineDatum(rebound, func(id ids.OID) ids.OID {
						if target, ok := mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}]; ok {
							return target.ObjectID
						}
						return id
					})
					if er != nil {
						return er
					}
					changed = changed || remapped != rebound
					rebound = remapped
				}
				if len(mapping) != 0 && !rebound.IsNull() && target.ContainsOperatorAlias() {
					remapped, er := remapOperatorDatum(rebound, func(id ids.OID) ids.OID {
						if target, ok := mapping[ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}]; ok {
							return target.ObjectID
						}
						return id
					})
					if er != nil {
						return er
					}
					changed = changed || remapped != rebound
					rebound = remapped
				}
				if len(mapping) != 0 && !rebound.IsNull() && target.ContainsRegTS() {
					remapped, er := remapRegTSDatum(rebound, func(address ids.ObjectAddress) ids.ObjectAddress {
						if target, ok := mapping[address]; ok {
							return target
						}
						return address
					})
					if er != nil {
						return er
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
			if values != nil {
				if versions == nil {
					versions = append([]storage.RowVersion(nil), relation.Versions...)
				}
				versions[i].Values = values
				versions[i].IndexOutputs = nil
			}
		}
		if versions != nil {
			copy := *relation
			copy.Versions = versions
			relations[address] = &copy
		}
	}
	return nil
}
