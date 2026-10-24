package catalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// BindingClaim is one name a statement's stage binds to address. Priority
// orders the diagnostics among held claims (types, relations, array types,
// then namespaces); RelationRename marks a relation rowtype renamed with its
// relation.
type BindingClaim struct {
	Key            ids.NameKey
	Address        ids.ObjectAddress
	Priority       int
	RelationRename bool
}

// BindingCurrent rechecks claim against current after its name lock is held:
// the name must still resolve as in base, or already to the claimed address.
// A vanished binding means the catalog was replaced; another binding is the
// unique-index violation PostgreSQL reports.
func BindingCurrent(base, current Read, claim BindingClaim) error {
	address := current.NameAddress(claim.Key)
	if address != base.NameAddress(claim.Key) && address != claim.Address {
		if address == (ids.ObjectAddress{}) {
			return ids.ErrCatalogReplaced
		}
		return bindingDuplicate(claim)
	}
	return nil
}

func bindingDuplicate(claim BindingClaim) error {
	key := claim.Key
	// Renaming/moving a table diagnoses the relation uniqueness index, even
	// when its reciprocal rowtype was the first canonical conflicting claim.
	if key.ClassID == ids.TypeClassID && claim.RelationRename {
		key.ClassID = ids.RelationClassID
	}
	table, constraint, detail := "pg_class", "pg_class_relname_nsp_index", fmt.Sprintf("Key (relname, relnamespace)=(%s, %d) already exists.", key.Name, key.Namespace.ObjectID)
	switch key.ClassID {
	case ids.NamespaceClassID:
		table, constraint, detail = "pg_namespace", "pg_namespace_nspname_index", fmt.Sprintf("Key (nspname)=(%s) already exists.", key.Name)
	case ids.TypeClassID:
		table, constraint, detail = "pg_type", "pg_type_typname_nsp_index", fmt.Sprintf("Key (typname, typnamespace)=(%s, %d) already exists.", key.Name, key.Namespace.ObjectID)
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: fmt.Sprintf("duplicate key value violates unique constraint %q", constraint), Detail: detail, Schema: "pg_catalog", Table: table, Constraint: constraint}
}
