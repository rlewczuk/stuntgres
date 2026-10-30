package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoForeignTablesOID is a native identity, not a PostgreSQL allocation.
const InfoForeignTablesOID ids.OID = 16248

var infoForeignTablesColumns = []ColumnSpec{
	{"foreign_table_catalog", 13248, true, 950, 0, -1},
	{"foreign_table_schema", 13248, true, 950, 0, -1},
	{"foreign_table_name", 13248, true, 950, 0, -1},
	{"foreign_server_catalog", 13248, true, 950, 0, -1},
	{"foreign_server_name", 13248, true, 950, 0, -1},
}

func captureInfoForeignTables(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing foreign_tables runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoForeignTablesOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || !in.Source.VirtualView || len(in.Source.Columns) != len(infoForeignTablesColumns) || len(in.Source.Attributes) != len(infoForeignTablesColumns) {
		return nil, fmt.Errorf("stuntgres: invalid foreign_tables source")
	}
	if in.Source.Name() != "foreign_tables" || in.Source.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}) || in.Source.Address().ClassID != ids.RelationClassID {
		return nil, fmt.Errorf("stuntgres: invalid foreign_tables identity")
	}
	for i, spec := range infoForeignTablesColumns {
		column := in.Source.Columns[i]
		domain, ok := in.Catalog.LookupType(spec.Type)
		base, baseOK := in.Catalog.LookupType(sqltype.Name)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || column.Name != spec.Name || column.Usage != domain.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != 0 || column.OriginTable != uint32(InfoForeignTablesOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid foreign_tables domain authority")
		}
	}
	identifier := in.Source.Columns[0].Usage.Type
	database, err := identifier.Wrap(sqltype.String(identifier.BaseType(), in.Runtime.DatabaseName()))
	if err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.ForeignRelation || relation.Foreign == nil {
			continue
		}
		_, visible, err := infoRelationVisible(in, relation)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		namespace, ok := in.Catalog.Namespace(relation.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing foreign_tables namespace")
		}
		// The table's stored association is authoritative. Server and wrapper
		// USAGE are not additional disclosure gates for an eligible table.
		server, ok := in.Catalog.Foreign(relation.Foreign.Server)
		if !ok || server.Address().ClassID != ids.ForeignServerClassID {
			return nil, fmt.Errorf("stuntgres: invalid foreign_tables server authority")
		}
		wrapper, ok := in.Catalog.Foreign(server.Wrapper)
		if !ok || wrapper.Address().ClassID != ids.ForeignWrapperClassID {
			return nil, fmt.Errorf("stuntgres: invalid foreign_tables wrapper authority")
		}
		schema, err := identifier.Wrap(sqltype.String(identifier.BaseType(), namespace.Name()))
		if err != nil {
			return nil, err
		}
		name, err := identifier.Wrap(sqltype.String(identifier.BaseType(), relation.Name()))
		if err != nil {
			return nil, err
		}
		serverName, err := identifier.Wrap(sqltype.String(identifier.BaseType(), server.Name()))
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{database, schema, name, database, serverName})
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
