package engine

import (
	"context"
	"fmt"
	"sort"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const pgUserMappingsOID ids.OID = 12095

func (e *Engine) initializeForeignCatalogSources() error {
	type column struct {
		name     string
		typ      sqltype.ID
		nullable bool
	}
	for _, spec := range []struct {
		oid     ids.OID
		name    string
		columns []column
	}{
		{ids.ForeignWrapperClassID, "pg_foreign_data_wrapper", []column{{"oid", sqltype.Oid, false}, {"fdwname", sqltype.Name, false}, {"fdwowner", sqltype.Oid, false}, {"fdwhandler", sqltype.Oid, false}, {"fdwvalidator", sqltype.Oid, false}, {"fdwacl", sqltype.ACLItemArray, true}, {"fdwoptions", 1009, true}}},
		{ids.ForeignServerClassID, "pg_foreign_server", []column{{"oid", sqltype.Oid, false}, {"srvname", sqltype.Name, false}, {"srvowner", sqltype.Oid, false}, {"srvfdw", sqltype.Oid, false}, {"srvtype", sqltype.Text, true}, {"srvversion", sqltype.Text, true}, {"srvacl", sqltype.ACLItemArray, true}, {"srvoptions", 1009, true}}},
		{ids.ForeignMappingClassID, "pg_user_mapping", []column{{"oid", sqltype.Oid, false}, {"umuser", sqltype.Oid, false}, {"umserver", sqltype.Oid, false}, {"umoptions", 1009, true}}},
		{ids.ForeignTableClassID, "pg_foreign_table", []column{{"ftrelid", sqltype.Oid, false}, {"ftserver", sqltype.Oid, false}, {"ftoptions", 1009, true}}},
		{pgUserMappingsOID, "pg_user_mappings", []column{{"umid", sqltype.Oid, false}, {"srvid", sqltype.Oid, false}, {"srvname", sqltype.Name, false}, {"umuser", sqltype.Oid, false}, {"usename", sqltype.Name, true}, {"umoptions", 1009, true}}},
	} {
		d := e.catalogSourceDescriptor(spec.oid, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, spec.name, len(spec.columns))
		if spec.oid == ids.ForeignMappingClassID {
			d.ACL = authorization.ACLValue{}
		}
		d.VirtualView = spec.oid == pgUserMappingsOID
		for i, column := range spec.columns {
			typ, ok := (e.catalog.read(e.catalog.state)).LookupType(column.typ)
			if !ok {
				return fmt.Errorf("stuntgres: missing SQL/MED catalog type %d", column.typ)
			}
			d.Columns[i] = ColumnDescriptor{Name: column.name, Usage: typ.Usage(), Nullable: column.nullable, NullabilityKnown: true, OriginTable: uint32(spec.oid), OriginAttribute: int16(i + 1)}
			if column.typ == sqltype.Text || column.typ == 1009 {
				d.Columns[i].Usage.Collation.ID = 950
			}
			d.Attributes[i] = int32(i + 1)
			d.Index[column.name] = i
		}
		e.registerCatalogSource(d)
	}
	return nil
}

func foreignOptionsValue(typ *sqltype.Descriptor, options []ForeignOption) (sqltype.Value, error) {
	if len(options) == 0 {
		return sqltype.Null(typ), nil
	}
	elements := make([]sqltype.Value, len(options))
	for i, option := range options {
		elements[i] = sqltype.String(typ.Element(), option.Name+"="+option.Value)
	}
	return sqltype.OwnedArrayValue(typ, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
}

func (e *Engine) captureForeignCatalog(ctx context.Context, auth AuthorizationView, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	var descriptors []Descriptor
	for _, d := range auth.catalog.Descriptors() {
		if foreign, ok := d.(*foreignDescriptor); ok {
			if foreign.address.ClassID == source.address.ObjectID || source.address.ObjectID == pgUserMappingsOID && foreign.address.ClassID == ids.ForeignMappingClassID {
				descriptors = append(descriptors, d)
			}
		} else if r, ok := d.(*relationDescriptor); ok && source.address.ObjectID == ids.ForeignTableClassID && r.Foreign != nil {
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
		if relation, ok := descriptor.(*relationDescriptor); ok {
			if err = putOID(0, uint32(relation.address.ObjectID)); err != nil {
				return nil, err
			}
			if err = putOID(1, uint32(relation.Foreign.Server.ObjectID)); err != nil {
				return nil, err
			}
			row[2], err = foreignOptionsValue(source.Columns[2].Usage.Type, relation.Foreign.Options)
		} else {
			d := descriptor.(*foreignDescriptor)
			if err = putOID(0, uint32(d.address.ObjectID)); err != nil {
				return nil, err
			}
			switch source.address.ObjectID {
			case ids.ForeignWrapperClassID:
				putString(1, d.name)
				for i, value := range []uint32{uint32(d.Owner.ID()), uint32(d.Handler.ObjectID), uint32(d.Validator.ObjectID)} {
					if err = putOID(i+2, value); err != nil {
						return nil, err
					}
				}
				row[5], err = e.pgClassACL(auth.roles, source.Columns[5].Usage.Type, d.ACL)
				if err != nil {
					return nil, err
				}
				row[6], err = foreignOptionsValue(source.Columns[6].Usage.Type, d.Options)
			case ids.ForeignServerClassID:
				putString(1, d.name)
				if err = putOID(2, uint32(d.Owner.ID())); err != nil {
					return nil, err
				}
				if err = putOID(3, uint32(d.Wrapper.ObjectID)); err != nil {
					return nil, err
				}
				if d.ServerType != nil {
					putString(4, *d.ServerType)
				}
				if d.Version != nil {
					putString(5, *d.Version)
				}
				row[6], err = e.pgClassACL(auth.roles, source.Columns[6].Usage.Type, d.ACL)
				if err != nil {
					return nil, err
				}
				row[7], err = foreignOptionsValue(source.Columns[7].Usage.Type, d.Options)
			case ids.ForeignMappingClassID:
				if err = putOID(1, uint32(d.MappedRole.ID())); err != nil {
					return nil, err
				}
				if err = putOID(2, uint32(d.Server.ObjectID)); err != nil {
					return nil, err
				}
				row[3], err = foreignOptionsValue(source.Columns[3].Usage.Type, d.Options)
			case pgUserMappingsOID:
				server, ok := auth.catalog.Foreign(d.Server)
				if !ok {
					return nil, ids.ErrCatalogReplaced
				}
				if err = putOID(1, uint32(server.address.ObjectID)); err != nil {
					return nil, err
				}
				putString(2, server.name)
				if err = putOID(3, uint32(d.MappedRole.ID())); err != nil {
					return nil, err
				}
				if d.MappedRole == (authorization.PrincipalRef{}) {
					putString(4, "public")
				} else if role, ok := auth.roles.Find(d.MappedRole); ok {
					putString(4, role.Name)
				}
				actor, er := auth.roles.Descriptor(auth.state.CurrentUser)
				if er != nil {
					return nil, er
				}
				owner := auth.Check(AccessRequirement{Object: foreignRef(auth, server), OwnerOnly: true}) == nil
				visible := actor.Superuser || d.MappedRole == (authorization.PrincipalRef{}) && owner
				if d.MappedRole == auth.state.CurrentUser {
					visible = visible || owner || auth.Check(AccessRequirement{Object: foreignRef(auth, server), Privileges: authorization.UsagePrivilege}) == nil
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
	return &querySource{ref: ref, anonymous: rows}, nil
}
