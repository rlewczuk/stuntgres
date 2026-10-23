package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoDomainConstraintsOID is the native virtual relation identity, independent
// of PostgreSQL's allocator-dependent information-schema view OID.
const InfoDomainConstraintsOID ids.OID = 16211

// captureInfoDomainConstraints projects actual own domain CHECK and NOT NULL
// definitions. Ancestor definitions remain on their owning domain, and neither
// validation state nor schema USAGE changes this view's eight-column contract.
func captureInfoDomainConstraints(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoDomainConstraintsOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 8 {
		return nil, fmt.Errorf("stuntgres: invalid domain_constraints source")
	}
	for i, column := range in.Source.Columns {
		domain, base := sqltype.ID(13248), sqltype.Name
		if i >= 6 {
			domain, base = 13256, sqltype.VarChar
		}
		typ, ok := in.Catalog.LookupType(domain)
		physical, present := in.Catalog.LookupType(base)
		if !ok || !present || !typ.IsDomain() || typ.BaseType() != physical || column.Usage != typ.Usage() {
			return nil, fmt.Errorf("stuntgres: invalid domain_constraints source domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		domain, ok := descriptor.(*catalog.Type)
		if !ok || !domain.SQLType.IsDomain() || len(domain.DomainChecks) == 0 && domain.DomainNotNull == nil {
			continue
		}
		visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), domain.Owner)
		if err != nil {
			return nil, err
		}
		if !visible {
			rights, err := in.Runtime.Effective(in.Ref.Domain().Ref(in.Ref.Epoch, domain.Address()), false)
			if err != nil {
				return nil, err
			}
			if rights&authorization.UsagePrivilege == 0 {
				continue
			}
		}
		namespace, ok := in.Catalog.Namespace(domain.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing domain_constraints namespace")
		}
		emit := func(oid ids.OID, name string) error {
			if err := in.Context.Err(); err != nil {
				return err
			}
			if oid == 0 || name == "" {
				return fmt.Errorf("stuntgres: invalid domain_constraints definition")
			}
			row := make([]sqltype.Value, 8)
			for i, text := range [8]string{database, namespace.Name(), name, database, namespace.Name(), domain.Name(), "NO", "NO"} {
				typ := in.Source.Columns[i].Usage.Type
				value, err := typ.Wrap(sqltype.String(typ.BaseType(), text))
				if err != nil {
					return err
				}
				row[i] = value
			}
			rows = append(rows, row)
			return nil
		}
		if nn := domain.DomainNotNull; nn != nil {
			if err := emit(nn.OID, nn.Name); err != nil {
				return nil, err
			}
		}
		for i := range domain.DomainChecks {
			check := &domain.DomainChecks[i]
			if err := emit(check.OID, check.Name); err != nil {
				return nil, err
			}
		}
	}
	return rows, nil
}
