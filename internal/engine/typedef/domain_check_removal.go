package typedef

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// WithoutDomainCheck returns a copy of domain d without its CHECK constraint
// oid, with the domain SQL type rebuilt for the remaining checks. It keeps
// d's revision and never changes d.
func WithoutDomainCheck(d *catalog.Type, oid ids.OID) (*catalog.Type, error) {
	next := *d
	next.DomainChecks = make([]catalog.DomainCheckConstraint, 0, len(d.DomainChecks))
	for _, check := range d.DomainChecks {
		if check.OID != oid {
			next.DomainChecks = append(next.DomainChecks, check)
		}
	}
	var err error
	next.SQLType, err = sqltype.Domain(d.SQLType.ID(), d.Name(), d.SQLType.SQLName(), d.SQLType.DomainBase(), len(next.DomainChecks) != 0, d.SQLType.OwnDomainNotNull())
	if err != nil {
		return nil, err
	}
	return &next, nil
}
