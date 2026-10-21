package table

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
)

// NewRelation allocates a table descriptor: its OID, then its revision. The
// table identity owner is shared by declared and query-derived schemas and
// composite types. The caller transfers a private column slice; source
// definitions are not mutated.
func NewRelation(a ddl.Allocator, ns ids.ObjectAddress, name string, owner authorization.PrincipalRef, columns []catalog.Column, index map[string]int) (*catalog.Relation, error) {
	oid, err := a.AllocateOID()
	if err != nil {
		return nil, err
	}
	revision, err := a.Revision()
	if err != nil {
		return nil, err
	}
	if index == nil {
		index = make(map[string]int, len(columns))
		for i, column := range columns {
			index[column.Name] = i
		}
	}
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: ns, ObjectName: name, ObjectRevision: revision, Owner: owner, Columns: columns, Index: index, Attributes: make([]int32, len(columns))}
	d.ColumnMeta.HighWater = int32(len(columns))
	for i := range columns {
		d.Attributes[i] = int32(i + 1)
		d.Columns[i].OriginTable = uint32(oid)
		d.Columns[i].OriginAttribute = int16(i + 1)
	}
	return d, nil
}

// AssignDefinitionOIDs gives every NOT NULL, DEFAULT, generation, CHECK,
// UNIQUE and FOREIGN KEY definition of d without an OID one, in that order.
func AssignDefinitionOIDs(a ddl.Allocator, d *catalog.Relation) error {
	for i := range d.Constraints.NotNull {
		if d.Constraints.NotNull[i].OID == 0 {
			id, err := a.AllocateOID()
			if err != nil {
				return err
			}
			d.Constraints.NotNull[i].OID = id
		}
	}
	for _, attr := range d.Attributes {
		def, ok := d.ColumnMeta.Defaults[attr]
		if ok && def.OID == 0 {
			id, err := a.AllocateOID()
			if err != nil {
				return err
			}
			def.OID = id
			d.ColumnMeta.Defaults[attr] = def
		}
	}
	for _, attr := range d.Attributes {
		definition, exists := d.ColumnMeta.Generations[attr]
		if exists && definition.OID == 0 {
			id, err := a.AllocateOID()
			if err != nil {
				return err
			}
			definition.OID = id
			d.ColumnMeta.Generations[attr] = definition
		}
	}
	for i := range d.Constraints.Check {
		if d.Constraints.Check[i].OID == 0 {
			id, err := a.AllocateOID()
			if err != nil {
				return err
			}
			d.Constraints.Check[i].OID = id
		}
	}
	for i := range d.Constraints.Unique {
		if d.Constraints.Unique[i].OID == 0 {
			id, err := a.AllocateOID()
			if err != nil {
				return err
			}
			d.Constraints.Unique[i].OID = id
		}
	}
	for i := range d.Constraints.ForeignKeys {
		if d.Constraints.ForeignKeys[i].OID == 0 {
			id, err := a.AllocateOID()
			if err != nil {
				return err
			}
			d.Constraints.ForeignKeys[i].OID = id
		}
	}
	return nil
}
