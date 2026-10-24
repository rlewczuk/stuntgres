package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoTablesOID is the reserved native relation identity, not a PostgreSQL allocation.
const InfoTablesOID ids.OID = 16217

var infoTablesColumns = []ColumnSpec{
	{"table_catalog", 13248, true, 950, 0, -1},
	{"table_schema", 13248, true, 950, 0, -1},
	{"table_name", 13248, true, 950, 0, -1},
	{"table_type", 13246, true, 950, 0, -1},
	{"self_referencing_column_name", 13248, true, 950, 0, -1},
	{"reference_generation", 13246, true, 950, 0, -1},
	{"user_defined_type_catalog", 13248, true, 950, 0, -1},
	{"user_defined_type_schema", 13248, true, 950, 0, -1},
	{"user_defined_type_name", 13248, true, 950, 0, -1},
	{"is_insertable_into", 13256, true, 950, 0, -1},
	{"is_typed", 13256, true, 950, 0, -1},
	{"commit_action", 13246, true, 950, 0, -1},
}

// The engine owns the capture lock. All metadata, privileges and structural
// capabilities use this one immutable catalog/role frontier; no relation runs.
func captureInfoTables(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing tables runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoTablesOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || in.Source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: InfoTablesOID}) || !in.Source.VirtualView || len(in.Source.Columns) != len(infoTablesColumns) || len(in.Source.Attributes) != len(infoTablesColumns) {
		return nil, fmt.Errorf("stuntgres: invalid tables source")
	}
	for i, spec := range infoTablesColumns {
		column := in.Source.Columns[i]
		typ, ok := in.Catalog.LookupType(spec.Type)
		if !ok || !typ.IsDomain() {
			return nil, fmt.Errorf("stuntgres: missing tables domain")
		}
		baseID := sqltype.VarChar
		if spec.Type == 13248 {
			baseID = sqltype.Name
		}
		base, ok := in.Catalog.LookupType(baseID)
		if !ok || typ.BaseType() != base || column.Name != spec.Name || column.Usage != typ.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != spec.Dimensions || column.OriginTable != uint32(InfoTablesOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid tables domain authority")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	temporary := in.Runtime.TemporaryOwner()
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		kind := ClassKind(in.Catalog, relation)
		if kind != "r" && kind != "p" && kind != "v" && kind != "f" {
			continue
		}
		namespace, ok := in.Catalog.Namespace(relation.Namespace())
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		isTemporary := namespace.Temporary != (ids.SessionToken{})
		if isTemporary && namespace.Temporary != temporary {
			continue
		}
		_, visible, err := infoRelationVisible(in, relation)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		tableType, insertable := "BASE TABLE", "YES"
		if kind == "v" || kind == "f" {
			tableType = "VIEW"
			if kind == "f" {
				tableType = "FOREIGN"
			}
			facts, err := in.Runtime.ViewCapabilities(in.Context, in.Catalog, relation)
			if err != nil {
				return nil, err
			}
			if facts.Events&8 == 0 {
				insertable = "NO"
			}
		}
		if isTemporary {
			tableType = "LOCAL TEMPORARY"
		}
		texts := [12]string{database, namespace.Name(), relation.Name(), tableType, "", "", "", "", "", insertable, "NO", ""}
		typed := relation.OfType != (ids.ObjectAddress{})
		if typed {
			typ, ok := in.Catalog.Type(relation.OfType)
			if !ok {
				return nil, ids.ErrCatalogReplaced
			}
			typeNamespace, ok := in.Catalog.Namespace(typ.Namespace())
			if !ok {
				return nil, ids.ErrCatalogReplaced
			}
			texts[6], texts[7], texts[8], texts[10] = database, typeNamespace.Name(), typ.Name(), "YES"
		}
		row := make([]sqltype.Value, 12)
		for i, text := range texts {
			typ := in.Source.Columns[i].Usage.Type
			if i == 4 || i == 5 || i == 11 || !typed && i >= 6 && i <= 8 {
				row[i] = sqltype.Null(typ)
				continue
			}
			row[i], err = typ.Wrap(sqltype.String(typ.BaseType(), text))
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
