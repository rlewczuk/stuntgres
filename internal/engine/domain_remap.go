package engine

import (
	"fmt"
	"strconv"

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

// Remap captured relation identities recursively without rendering or reparsing
// array text. Payloads are copied only on the first affected element.
func remapRegclassDatum(value sqltype.Value, destination func(ids.OID) ids.OID) (sqltype.Value, error) {
	if value.IsNull() {
		return value, nil
	}
	if value.Type().BaseType().IsComposite() {
		return value.MapComposite(value.Type(), func(member sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return remapRegclassDatum(member, destination)
		})
	}
	if value.Type().BaseType().IsArray() {
		return value.MapArray(value.Type(), func(element sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return remapRegclassDatum(element, destination)
		})
	}
	if value.Type().BaseType().ID() != sqltype.Regclass {
		return value, nil
	}
	physical, err := value.BaseValue()
	if err != nil {
		return sqltype.Value{}, err
	}
	text, err := physical.Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	oid, err := strconv.ParseUint(text, 10, 32)
	if err != nil {
		return sqltype.Value{}, err
	}
	target := destination(ids.OID(oid))
	if target == ids.OID(oid) {
		return value, nil
	}
	physical = sqltype.String(value.Type().BaseType(), strconv.FormatUint(uint64(target), 10))
	if value.Type().IsDomain() {
		return value.Type().Wrap(physical)
	}
	return physical, nil
}
