package access

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

// PublicationDatabaseCreate keeps normal database ACL authority, with the
// publication command family's database diagnostic (an unquoted database name).
func PublicationDatabaseCreate(v View, env Environment) error {
	err := OwnershipDatabaseCreate(v, env)
	if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
		copy := *diagnostic
		copy.Message = "permission denied for database " + v.Catalog.MustDatabase(v.Owner.DatabaseAddress()).Name()
		return &copy
	}
	return err
}

// PublicationOwnershipEligibility is shared by ALTER OWNER and REASSIGN OWNED.
// The target's database privilege, not the caller's, governs actual transfers.
func PublicationOwnershipEligibility(v View, env Environment, d *catalog.Publication, target authorization.PrincipalRef) error {
	if err := v.Check(env, Requirement{Object: v.Ref(d.Address()), OwnerOnly: true, OwnerNoun: "publication"}); err != nil {
		return err
	}
	if target == d.Owner {
		return nil
	}
	actor, err := v.Roles.Descriptor(v.State.CurrentUser)
	if err != nil {
		return err
	}
	if actor.Superuser {
		return nil
	}
	if err = OwnershipSuccessor(v, target); err != nil {
		return err
	}
	recipient := v
	recipient.State.CurrentUser = target
	if err = PublicationDatabaseCreate(recipient, env); err != nil {
		return err
	}
	role, err := v.Roles.Descriptor(target)
	if err != nil {
		return err
	}
	if role.Superuser {
		return nil
	}
	hint := ""
	if d.AllTables {
		hint = "The owner of a FOR ALL TABLES publication must be a superuser."
	} else {
		for _, n := range v.Catalog.PublicationNamespaces() {
			if n.Publication == d.Address() {
				hint = "The owner of a FOR TABLES IN SCHEMA publication must be a superuser."
				break
			}
		}
	}
	if hint != "" {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`permission denied to change owner of publication "%s"`, d.Name()), Hint: hint}
	}
	return nil
}
