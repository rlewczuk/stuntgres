package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

var pgForeignServerColumns = []ColumnSpec{
	{"oid", sqltype.Oid, false, 0, 0, -1},
	{"srvname", sqltype.Name, false, 0, 0, -1},
	{"srvowner", sqltype.Oid, false, 0, 0, -1},
	{"srvfdw", sqltype.Oid, false, 0, 0, -1},
	{"srvtype", sqltype.Text, true, 950, 0, -1},
	{"srvversion", sqltype.Text, true, 950, 0, -1},
	{"srvacl", sqltype.ACLItemArray, true, 0, 1, -1},
	{"srvoptions", 1009, true, 950, 1, -1},
}

// Base catalog visibility is independent of server USAGE. The source binder
// owns relation/column SELECT admission; this projection never executes a provider.
func capturePGForeignServer(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing pg_foreign_server runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, ids.ForeignServerClassID); err != nil {
		return nil, err
	}
	view, source, roles := in.Catalog, in.Source, in.Roles
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.ForeignServerClassID}
	if in.Owner == nil || view.Owner() != in.Owner || view.State() == nil || source == nil || source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != view.Epoch() || in.Ref.Address != address || view.Get(address) != source {
		return nil, fmt.Errorf("stuntgres: invalid pg_foreign_server authority")
	}
	if source.Name() != "pg_foreign_server" || source.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || source.RelationKind != catalog.CatalogRelation || source.VirtualView || source.CatalogView || len(source.Columns) != 8 || len(source.Attributes) != 8 || len(source.ColumnMeta.CatalogAttributes) != 8 {
		return nil, fmt.Errorf("stuntgres: invalid pg_foreign_server source")
	}
	for i, field := range pgForeignServerColumns {
		column := source.Columns[i]
		typ, ok := view.LookupType(field.Type)
		if !ok || typ != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign pg_foreign_server type")
		}
		usage := typ.Usage()
		if field.Collation != 0 {
			usage.Collation.ID = field.Collation
		}
		fact := source.ColumnMeta.CatalogAttributes[i]
		if fact.NotNull != !field.Nullable || fact.Dimensions != field.Dimensions || fact.Collation != usage.Collation.ID {
			return nil, fmt.Errorf("stuntgres: invalid pg_foreign_server attribute facts")
		}
		if column.Name != field.Name || column.Usage != usage || column.Usage.Typmod != -1 || column.Nullable != field.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != field.Dimensions || column.OriginTable != uint32(address.ObjectID) || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid pg_foreign_server shape")
		}
	}
	database, ok := view.Database(in.Owner.DatabaseAddress())
	if !ok || database == nil || !roles.BelongsTo(in.Owner.Principals) || roles.DatabaseOwner() != database.Owner {
		return nil, fmt.Errorf("stuntgres: foreign pg_foreign_server role authority")
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range view.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		server, ok := descriptor.(*catalog.ForeignObject)
		if !ok || server.Address().ClassID != ids.ForeignServerClassID {
			continue
		}
		if err := (catalog.ForeignCatalogProvider{Class: ids.ForeignServerClassID}).Validate(view, server); err != nil {
			return nil, err
		}
		if _, err := roles.Descriptor(server.Owner); err != nil {
			return nil, err
		}
		if err := in.Runtime.ValidateACL(roles, server); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 8)
		for i, column := range source.Columns {
			row[i] = sqltype.Null(column.Usage.Type)
		}
		for i, oid := range [4]uint32{uint32(server.Address().ObjectID), 0, uint32(server.Owner.ID()), uint32(server.Wrapper.ObjectID)} {
			if i == 1 {
				row[i] = sqltype.String(source.Columns[i].Usage.Type, server.Name())
				continue
			}
			value, err := sqltype.OIDValue(source.Columns[i].Usage.Type, oid)
			if err != nil {
				return nil, err
			}
			row[i] = value
		}
		if server.ServerType != nil {
			row[4] = sqltype.String(source.Columns[4].Usage.Type, *server.ServerType)
		}
		if server.Version != nil {
			row[5] = sqltype.String(source.Columns[5].Usage.Type, *server.Version)
		}
		var err error
		if server.ACL.Explicit && len(server.ACL.Entries) == 0 {
			row[6], err = sqltype.EmptyACLArrayValue(source.Columns[6].Usage.Type)
		} else {
			row[6], err = ClassACL(in.Owner, roles, source.Columns[6].Usage.Type, server.ACL)
		}
		if err != nil {
			return nil, err
		}
		row[7], err = foreignOptionsValue(source.Columns[7].Usage.Type, server.Options)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
