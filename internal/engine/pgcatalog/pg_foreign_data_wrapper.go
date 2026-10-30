package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

var pgForeignDataWrapperColumns = []ColumnSpec{
	{"oid", sqltype.Oid, false, 0, 0, -1},
	{"fdwname", sqltype.Name, false, 0, 0, -1},
	{"fdwowner", sqltype.Oid, false, 0, 0, -1},
	{"fdwhandler", sqltype.Oid, false, 0, 0, -1},
	{"fdwvalidator", sqltype.Oid, false, 0, 0, -1},
	{"fdwacl", sqltype.ACLItemArray, true, 0, 1, -1},
	{"fdwoptions", 1009, true, 950, 1, -1},
}

// Base catalog visibility is independent of wrapper USAGE. The source binder
// owns relation/column SELECT admission; this projection never executes a provider.
func capturePGForeignDataWrapper(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing pg_foreign_data_wrapper runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, ids.ForeignWrapperClassID); err != nil {
		return nil, err
	}
	view, source, roles := in.Catalog, in.Source, in.Roles
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.ForeignWrapperClassID}
	if in.Owner == nil || view.Owner() != in.Owner || view.State() == nil || source == nil || source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != view.Epoch() || in.Ref.Address != address || view.Get(address) != source {
		return nil, fmt.Errorf("stuntgres: invalid pg_foreign_data_wrapper authority")
	}
	if source.Name() != "pg_foreign_data_wrapper" || source.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || source.RelationKind != catalog.CatalogRelation || source.VirtualView || source.CatalogView || len(source.Columns) != 7 || len(source.Attributes) != 7 || len(source.ColumnMeta.CatalogAttributes) != 7 {
		return nil, fmt.Errorf("stuntgres: invalid pg_foreign_data_wrapper source")
	}
	for i, field := range pgForeignDataWrapperColumns {
		column := source.Columns[i]
		typ, ok := view.LookupType(field.Type)
		if !ok || typ != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign pg_foreign_data_wrapper type")
		}
		usage := typ.Usage()
		if field.Collation != 0 {
			usage.Collation.ID = field.Collation
		}
		fact := source.ColumnMeta.CatalogAttributes[i]
		if fact.NotNull != !field.Nullable || fact.Dimensions != field.Dimensions || fact.Collation != usage.Collation.ID {
			return nil, fmt.Errorf("stuntgres: invalid pg_foreign_data_wrapper attribute facts")
		}
		if column.Name != field.Name || column.Usage != usage || column.Usage.Typmod != -1 || column.Nullable != field.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != field.Dimensions || column.OriginTable != uint32(address.ObjectID) || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid pg_foreign_data_wrapper shape")
		}
	}
	database, ok := view.Database(in.Owner.DatabaseAddress())
	if !ok || database == nil || !roles.BelongsTo(in.Owner.Principals) || roles.DatabaseOwner() != database.Owner {
		return nil, fmt.Errorf("stuntgres: foreign pg_foreign_data_wrapper role authority")
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range view.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		wrapper, ok := descriptor.(*catalog.ForeignObject)
		if !ok || wrapper.Address().ClassID != ids.ForeignWrapperClassID {
			continue
		}
		if err := (catalog.ForeignCatalogProvider{Class: ids.ForeignWrapperClassID}).Validate(view, wrapper); err != nil {
			return nil, err
		}
		if _, err := roles.Descriptor(wrapper.Owner); err != nil {
			return nil, err
		}
		if err := in.Runtime.ValidateACL(roles, wrapper); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 7)
		for i, oid := range [5]uint32{uint32(wrapper.Address().ObjectID), 0, uint32(wrapper.Owner.ID()), uint32(wrapper.Handler.ObjectID), uint32(wrapper.Validator.ObjectID)} {
			if i == 1 {
				row[i] = sqltype.String(source.Columns[i].Usage.Type, wrapper.Name())
				continue
			}
			value, err := sqltype.OIDValue(source.Columns[i].Usage.Type, oid)
			if err != nil {
				return nil, err
			}
			row[i] = value
		}
		var err error
		if wrapper.ACL.Explicit && len(wrapper.ACL.Entries) == 0 {
			row[5], err = sqltype.EmptyACLArrayValue(source.Columns[5].Usage.Type)
		} else {
			row[5], err = ClassACL(in.Owner, roles, source.Columns[5].Usage.Type, wrapper.ACL)
		}
		if err != nil {
			return nil, err
		}
		row[6], err = foreignOptionsValue(source.Columns[6].Usage.Type, wrapper.Options)
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
