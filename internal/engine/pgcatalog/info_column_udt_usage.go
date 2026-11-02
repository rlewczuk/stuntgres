package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoColumnUdtUsageOID is the native source identity, distinct from the
// canonical row type and PostgreSQL's allocator-dependent relation identity.
const InfoColumnUdtUsageOID ids.OID = 16260

// captureInfoColumnUdtUsage projects actual declarations through one domain hop.
// Enabled ownership of the resolved type, not relation or type ACLs, admits rows.
func captureInfoColumnUdtUsage(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoColumnUdtUsageOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 7 || len(in.Source.Attributes) != 7 {
		return nil, fmt.Errorf("stuntgres: invalid column_udt_usage source")
	}
	identifier, ok := in.Catalog.LookupType(13248)
	physical, present := in.Catalog.LookupType(sqltype.Name)
	if !ok || !present || !identifier.IsDomain() || identifier.DomainBase().Type != physical {
		return nil, fmt.Errorf("stuntgres: invalid column_udt_usage source domain")
	}
	for i, column := range in.Source.Columns {
		if column.Usage != identifier.Usage() || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid column_udt_usage source domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		switch ClassKind(in.Catalog, relation) {
		case "r", "v", "f", "p":
		default:
			continue
		}
		if len(relation.Columns) != len(relation.Attributes) {
			return nil, fmt.Errorf("stuntgres: invalid column_udt_usage attributes")
		}
		for i, column := range relation.Columns {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			attribute := relation.Attributes[i]
			if _, dropped := relation.ColumnMeta.Dropped[attribute]; attribute <= 0 || dropped {
				continue
			}
			facts, err := InfoColumnTypeFacts(in.Catalog, column.Usage)
			if err != nil {
				return nil, err
			}
			typ := facts.UDT
			visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), typ.Owner)
			if err != nil {
				return nil, err
			}
			if !visible {
				continue
			}
			typeNamespace, typeOK := in.Catalog.Namespace(typ.Namespace())
			tableNamespace, tableOK := in.Catalog.Namespace(relation.Namespace())
			if !typeOK || !tableOK {
				return nil, fmt.Errorf("stuntgres: missing column_udt_usage namespace")
			}
			row := make([]sqltype.Value, 7)
			for j, text := range [7]string{database, typeNamespace.Name(), typ.Name(), database, tableNamespace.Name(), relation.Name(), column.Name} {
				value, err := identifier.Wrap(sqltype.String(physical, text))
				if err != nil {
					return nil, err
				}
				row[j] = value
			}
			rows = append(rows, row)
		}
	}
	return rows, nil
}
