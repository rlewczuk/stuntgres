package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/sqltype"
)

func reownDomainTypes(next *catalogState, mapping map[ObjectAddress]ObjectAddress) (bool, error) {
	visited := make(map[ObjectAddress]bool)
	active := make(map[ObjectAddress]bool)
	changed := false
	var visit func(ObjectAddress) error
	visit = func(address ObjectAddress) error {
		if visited[address] {
			return nil
		}
		if active[address] {
			return fmt.Errorf("stuntgres: cyclic domain base")
		}
		d, ok := next.descriptors[address].(*typeDescriptor)
		if !ok {
			return fmt.Errorf("stuntgres: missing domain base")
		}
		if !d.typ.IsDomain() {
			visited[address] = true
			return nil
		}
		active[address] = true
		base := d.typ.DomainBase()
		baseAddress := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(base.Type.ID())}
		if err := visit(baseAddress); err != nil {
			return err
		}
		base.Type = next.descriptors[baseAddress].(*typeDescriptor).typ
		target := address
		if destination, ok := mapping[address]; ok {
			target = destination
		}
		namespace := d.Namespace()
		if destination, ok := mapping[namespace]; ok {
			namespace = destination
		}
		ns, ok := next.descriptors[d.Namespace()]
		if !ok {
			return fmt.Errorf("stuntgres: missing domain namespace")
		}
		declaration := dependencyIdentifier(ns.Name()) + "." + dependencyIdentifier(d.Name())
		if base.Type != d.typ.DomainBase().Type || target != address || namespace != d.Namespace() || declaration != d.typ.SQLName() {
			typ, err := sqltype.Domain(sqltype.ID(target.ObjectID), d.Name(), declaration, base)
			if err != nil {
				return err
			}
			copy := *d
			copy.typ = typ
			copy.namespace = namespace
			next.descriptors[address] = &copy
			changed = true
		}
		delete(active, address)
		visited[address] = true
		return nil
	}
	for address, d := range next.descriptors {
		if t, ok := d.(*typeDescriptor); ok && t.typ.IsDomain() {
			if err := visit(address); err != nil {
				return false, err
			}
		}
	}
	return changed, nil
}

// Only an explicit catalog identity mapping can reidentify a domain. Ordinary
// Value.Retag remains a checked same-logical-ID generation reownership operation.
func reownDomainDatum(value sqltype.Value, target *sqltype.Descriptor) (sqltype.Value, error) {
	if value.Type().ID() == target.ID() {
		return value.Retag(target)
	}
	if !value.Type().IsDomain() || !target.IsDomain() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: incompatible imported scalar identity")
	}
	from, to := value.Type().Usage().BaseUsage(), target.Usage().BaseUsage()
	if from.Type.ID() != to.Type.ID() || from.Typmod != to.Typmod || from.Collation != to.Collation {
		return sqltype.Value{}, fmt.Errorf("stuntgres: incompatible imported domain base")
	}
	physical, err := value.BaseValue()
	if err != nil {
		return sqltype.Value{}, err
	}
	physical, err = physical.Retag(to.Type)
	if err != nil {
		return sqltype.Value{}, err
	}
	return target.Wrap(physical)
}

func reownCatalogScalarTypes(next *catalogState, storage map[ObjectAddress]*relationStorage, mapping map[ObjectAddress]ObjectAddress) error {
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if d, ok := next.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(old.ID())}].(*typeDescriptor); ok {
			return d.typ
		}
		return nil
	}
	for address, descriptor := range next.descriptors {
		d, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		var columns []ColumnDescriptor
		for i, c := range d.columns {
			target := typ(c.Usage.Type)
			if target == nil {
				return fmt.Errorf("stuntgres: missing imported column type")
			}
			if target != c.Usage.Type {
				if columns == nil {
					columns = append([]ColumnDescriptor(nil), d.columns...)
				}
				columns[i].Usage.Type = target
			}
		}
		if columns != nil {
			copy := *d
			copy.columns = columns
			next.descriptors[address] = &copy
		}
	}
	for address, relation := range storage {
		var versions []rowVersion
		for i, row := range relation.versions {
			var values []sqltype.Value
			for j, value := range row.values {
				target := typ(value.Type())
				if target == nil {
					return fmt.Errorf("stuntgres: missing imported datum type")
				}
				rebound := value
				changed := target != value.Type()
				var err error
				if changed {
					rebound, err = reownDomainDatum(value, target)
					if err != nil {
						return err
					}
				}
				if len(mapping) != 0 && !rebound.IsNull() && target.BaseType().ID() == sqltype.Regclass {
					physical, er := rebound.BaseValue()
					if er != nil {
						return er
					}
					text, er := physical.Text()
					if er != nil {
						return er
					}
					oid, er := strconv.ParseUint(text, 10, 32)
					if er != nil {
						return er
					}
					if destination, ok := mapping[ObjectAddress{ClassID: RelationClassID, ObjectID: OID(oid)}]; ok {
						physical = sqltype.String(target.BaseType(), strconv.FormatUint(uint64(destination.ObjectID), 10))
						rebound = physical
						if target.IsDomain() {
							rebound, err = target.Wrap(physical)
							if err != nil {
								return err
							}
						}
						changed = true
					}
				}
				if changed {
					if values == nil {
						values = append([]sqltype.Value(nil), row.values...)
					}
					values[j] = rebound
				}
			}
			if values != nil {
				if versions == nil {
					versions = append([]rowVersion(nil), relation.versions...)
				}
				versions[i].values = values
			}
		}
		if versions != nil {
			copy := *relation
			copy.versions = versions
			storage[address] = &copy
		}
	}
	return nil
}
