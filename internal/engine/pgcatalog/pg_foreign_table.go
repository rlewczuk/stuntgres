package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

var pgForeignTableColumns = []ColumnSpec{
	{"ftrelid", sqltype.Oid, false, 0, 0, -1},
	{"ftserver", sqltype.Oid, false, 0, 0, -1},
	{"ftoptions", 1009, true, 950, 1, -1},
}

// Catalog SELECT admission belongs to the binder. Neither table privileges nor
// server USAGE filter these raw definitions, and no provider is executed here.
func capturePGForeignTable(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing pg_foreign_table runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, ids.ForeignTableClassID); err != nil {
		return nil, err
	}
	view, source, roles := in.Catalog, in.Source, in.Roles
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.ForeignTableClassID}
	if in.Owner == nil || view.Owner() != in.Owner || view.State() == nil || source == nil || source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != view.Epoch() || in.Ref.Address != address || view.Get(address) != source {
		return nil, fmt.Errorf("stuntgres: invalid pg_foreign_table authority")
	}
	if source.Name() != "pg_foreign_table" || source.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || source.RelationKind != catalog.CatalogRelation || source.VirtualView || source.CatalogView || len(source.Columns) != 3 || len(source.Attributes) != 3 || len(source.ColumnMeta.CatalogAttributes) != 3 {
		return nil, fmt.Errorf("stuntgres: invalid pg_foreign_table source")
	}
	for i, field := range pgForeignTableColumns {
		column := source.Columns[i]
		typ, ok := view.LookupType(field.Type)
		if !ok || typ != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign pg_foreign_table type")
		}
		usage := typ.Usage()
		if field.Collation != 0 {
			usage.Collation.ID = field.Collation
		}
		fact := source.ColumnMeta.CatalogAttributes[i]
		if fact.NotNull != !field.Nullable || fact.Dimensions != field.Dimensions || fact.Collation != usage.Collation.ID {
			return nil, fmt.Errorf("stuntgres: invalid pg_foreign_table attribute facts")
		}
		if column.Name != field.Name || column.Usage != usage || column.Usage.Typmod != -1 || column.Nullable != field.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != field.Dimensions || column.OriginTable != uint32(address.ObjectID) || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid pg_foreign_table shape")
		}
	}
	database, ok := view.Database(in.Owner.DatabaseAddress())
	if !ok || database == nil || !roles.BelongsTo(in.Owner.Principals) || roles.DatabaseOwner() != database.Owner {
		return nil, fmt.Errorf("stuntgres: foreign pg_foreign_table role authority")
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range view.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if relation.RelationKind != catalog.ForeignRelation {
			if relation.Foreign != nil {
				return nil, fmt.Errorf("stuntgres: nonforeign relation has foreign-table definition")
			}
			continue
		}
		a := relation.Address()
		if a.ClassID != ids.RelationClassID || a.ObjectID == 0 || a.SubID != 0 || relation.Revision() == 0 || view.Get(a) != relation || view.NameAddress(catalog.DescriptorNameKey(relation)) != a {
			return nil, fmt.Errorf("stuntgres: invalid foreign-table identity")
		}
		if _, ok := view.Namespace(relation.Namespace()); !ok || relation.Namespace().ClassID != ids.NamespaceClassID || relation.Namespace().ObjectID == 0 || relation.Namespace().SubID != 0 {
			return nil, fmt.Errorf("stuntgres: invalid foreign-table namespace")
		}
		if _, err := roles.Descriptor(relation.Owner); err != nil {
			return nil, err
		}
		if err := in.Runtime.ValidateACL(roles, relation); err != nil {
			return nil, err
		}
		// A CREATE/IMPORT table validator sees the authentic private relation
		// shell before foreign-table metadata is installed. Its shape remains
		// visible in pg_class, but there is no pg_foreign_table row yet.
		if relation.Foreign == nil {
			continue
		}
		serverAddress := relation.Foreign.Server
		server, ok := view.Foreign(serverAddress)
		if !ok || serverAddress.ClassID != ids.ForeignServerClassID || serverAddress.ObjectID == 0 || serverAddress.SubID != 0 || server.Address() != serverAddress {
			return nil, fmt.Errorf("stuntgres: invalid foreign-table server")
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
		row := make([]sqltype.Value, 3)
		for i, oid := range [2]uint32{uint32(a.ObjectID), uint32(serverAddress.ObjectID)} {
			value, err := sqltype.OIDValue(source.Columns[i].Usage.Type, oid)
			if err != nil {
				return nil, err
			}
			row[i] = value
		}
		var err error
		row[2], err = foreignOptionsValue(source.Columns[2].Usage.Type, relation.Foreign.Options)
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
