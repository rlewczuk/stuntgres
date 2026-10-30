package pgcatalog

import (
	"sort"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const UserMappingsOID ids.OID = 12095

func foreignOptionsValue(typ *sqltype.Descriptor, options []catalog.ForeignOption) (sqltype.Value, error) {
	if len(options) == 0 {
		return sqltype.Null(typ), nil
	}
	elements := make([]sqltype.Value, len(options))
	for i, option := range options {
		elements[i] = sqltype.String(typ.Element(), option.Name+"="+option.Value)
	}
	return sqltype.OwnedArrayValue(typ, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
}

func captureForeignCatalog(in Input) ([][]sqltype.Value, error) {
	ctx, source := in.Context, in.Source
	var descriptors []catalog.Descriptor
	for _, d := range in.Catalog.Descriptors() {
		if foreign, ok := d.(*catalog.ForeignObject); ok {
			if foreign.Address().ClassID == source.Address().ObjectID || source.Address().ObjectID == UserMappingsOID && foreign.Address().ClassID == ids.ForeignMappingClassID {
				descriptors = append(descriptors, d)
			}
		} else if r, ok := d.(*catalog.Relation); ok && source.Address().ObjectID == ids.ForeignTableClassID && r.Foreign != nil {
			descriptors = append(descriptors, d)
		}
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Address().ObjectID < descriptors[j].Address().ObjectID })
	rows := make([][]sqltype.Value, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, len(source.Columns))
		for i, c := range source.Columns {
			row[i] = sqltype.Null(c.Usage.Type)
		}
		putOID := func(index int, value uint32) error {
			v, err := sqltype.OIDValue(source.Columns[index].Usage.Type, value)
			row[index] = v
			return err
		}
		putString := func(index int, value string) { row[index] = sqltype.String(source.Columns[index].Usage.Type, value) }
		var err error
		if relation, ok := descriptor.(*catalog.Relation); ok {
			if err = putOID(0, uint32(relation.Address().ObjectID)); err != nil {
				return nil, err
			}
			if err = putOID(1, uint32(relation.Foreign.Server.ObjectID)); err != nil {
				return nil, err
			}
			row[2], err = foreignOptionsValue(source.Columns[2].Usage.Type, relation.Foreign.Options)
		} else {
			d := descriptor.(*catalog.ForeignObject)
			if err = putOID(0, uint32(d.Address().ObjectID)); err != nil {
				return nil, err
			}
			switch source.Address().ObjectID {
			case ids.ForeignMappingClassID:
				if err = putOID(1, uint32(d.MappedRole.ID())); err != nil {
					return nil, err
				}
				if err = putOID(2, uint32(d.Server.ObjectID)); err != nil {
					return nil, err
				}
				row[3], err = foreignOptionsValue(source.Columns[3].Usage.Type, d.Options)
			case UserMappingsOID:
				server, ok := in.Catalog.Foreign(d.Server)
				if !ok {
					return nil, ids.ErrCatalogReplaced
				}
				if err = putOID(1, uint32(server.Address().ObjectID)); err != nil {
					return nil, err
				}
				putString(2, server.Name())
				if err = putOID(3, uint32(d.MappedRole.ID())); err != nil {
					return nil, err
				}
				if d.MappedRole == (authorization.PrincipalRef{}) {
					putString(4, "public")
				} else if role, ok := in.Roles.Find(d.MappedRole); ok {
					putString(4, role.Name)
				}
				visible, er := in.Runtime.ForeignServerVisible(server, d.MappedRole)
				if er != nil {
					return nil, er
				}
				if visible {
					row[5], err = foreignOptionsValue(source.Columns[5].Usage.Type, d.Options)
				}
			}
		}
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}
