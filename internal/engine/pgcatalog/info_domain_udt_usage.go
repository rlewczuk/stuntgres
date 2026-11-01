package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoDomainUdtUsageOID is the native source identity, distinct from the
// canonical row type and allocator-dependent PostgreSQL relation identity.
const InfoDomainUdtUsageOID ids.OID = 16249

// captureInfoDomainUdtUsage projects actual domains' immediate base types.
// Enabled ownership of the base, not the domain or its USAGE ACL, admits a row.
func captureInfoDomainUdtUsage(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoDomainUdtUsageOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 6 {
		return nil, fmt.Errorf("stuntgres: invalid domain_udt_usage source")
	}
	identifier, ok := in.Catalog.LookupType(13248)
	physical, present := in.Catalog.LookupType(sqltype.Name)
	if !ok || !present || !identifier.IsDomain() || identifier.DomainBase().Type != physical {
		return nil, fmt.Errorf("stuntgres: invalid domain_udt_usage source domain")
	}
	for _, column := range in.Source.Columns {
		if column.Usage != identifier.Usage() {
			return nil, fmt.Errorf("stuntgres: invalid domain_udt_usage source domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	for address, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		domain, ok := descriptor.(*catalog.Type)
		if !ok || !domain.SQLType.IsDomain() || !domain.SQLType.Concrete() {
			continue
		}
		if address != domain.Address() {
			return nil, fmt.Errorf("stuntgres: invalid domain_udt_usage domain address")
		}
		if err := in.Runtime.ValidateType(in.Catalog, domain); err != nil {
			return nil, err
		}
		// DomainBase retains a nested domain's identity; BaseType would flatten
		// that relationship and incorrectly use the ultimate base's owner.
		usage := domain.SQLType.DomainBase()
		base, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
		if !ok || base.SQLType != usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign domain_udt_usage base authority")
		}
		domainNamespace, domainOK := in.Catalog.Namespace(domain.Namespace())
		baseNamespace, baseOK := in.Catalog.Namespace(base.Namespace())
		if !domainOK || !baseOK {
			return nil, fmt.Errorf("stuntgres: missing domain_udt_usage namespace")
		}
		visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), base.Owner)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		row := make([]sqltype.Value, 6)
		for i, text := range [6]string{database, baseNamespace.Name(), base.Name(), database, domainNamespace.Name(), domain.Name()} {
			value, err := identifier.Wrap(sqltype.String(physical, text))
			if err != nil {
				return nil, err
			}
			row[i] = value
		}
		rows = append(rows, row)
	}
	return rows, nil
}
