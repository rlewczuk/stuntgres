package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoColumnDomainUsageOID is the native source identity, independent of the
// allocator-dependent PostgreSQL view OID and the canonical row type identity.
const InfoColumnDomainUsageOID ids.OID = 16236

// captureInfoColumnDomainUsage projects live columns' direct declared domains.
// Enabled domain ownership, not domain USAGE or relation ACLs, controls visibility.
func captureInfoColumnDomainUsage(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoColumnDomainUsageOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 7 {
		return nil, fmt.Errorf("stuntgres: invalid column_domain_usage source")
	}
	identifier, ok := in.Catalog.LookupType(13248)
	physical, present := in.Catalog.LookupType(sqltype.Name)
	if !ok || !present || !identifier.IsDomain() || identifier.BaseType() != physical {
		return nil, fmt.Errorf("stuntgres: invalid column_domain_usage source domain")
	}
	for _, column := range in.Source.Columns {
		if column.Usage != identifier.Usage() {
			return nil, fmt.Errorf("stuntgres: invalid column_domain_usage source domain")
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
			return nil, fmt.Errorf("stuntgres: invalid column_domain_usage attributes")
		}
		for i, column := range relation.Columns {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			attribute := relation.Attributes[i]
			if _, dropped := relation.ColumnMeta.Dropped[attribute]; attribute <= 0 || dropped {
				continue
			}
			typ := column.Usage.Type
			if typ == nil || !typ.IsDomain() {
				continue
			}
			domain, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
			if !ok || domain.SQLType != typ {
				return nil, fmt.Errorf("stuntgres: invalid column_domain_usage declared domain")
			}
			visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), domain.Owner)
			if err != nil {
				return nil, err
			}
			if !visible {
				continue
			}
			domainNamespace, domainOK := in.Catalog.Namespace(domain.Namespace())
			tableNamespace, tableOK := in.Catalog.Namespace(relation.Namespace())
			if !domainOK || !tableOK {
				return nil, fmt.Errorf("stuntgres: missing column_domain_usage namespace")
			}
			row := make([]sqltype.Value, 7)
			for j, text := range [7]string{database, domainNamespace.Name(), domain.Name(), database, tableNamespace.Name(), relation.Name(), column.Name} {
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
