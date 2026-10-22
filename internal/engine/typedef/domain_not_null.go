package typedef

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
)

// BindDomainNotNull allocates one real domain-owned definition. Generated names
// share the namespace reservation with CHECKs; explicit names are domain-local.
func BindDomainNotNull(h StageHost, auth ddl.Authority, d *catalog.Type, name string) error {
	used := catalog.ConstraintNames(h.AccessView(auth).Catalog, d.Namespace(), nil)
	for _, check := range d.DomainChecks {
		if name != "" && name == check.Name {
			return sqlError("42710", fmt.Sprintf("constraint %q for domain %q already exists", name, d.Name()))
		}
		used[check.Name] = true
	}
	if name == "" {
		name = catalog.GeneratedConstraintName(d.Name(), "", "not_null", used)
	}
	oid, err := h.AllocateOID()
	if err != nil {
		return err
	}
	d.DomainNotNull = &catalog.DomainNotNullConstraint{OID: oid, Name: name}
	return nil
}
