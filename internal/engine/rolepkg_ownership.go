package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ownerChange is a decided ALTER ... OWNER: Object gets Target as its new
// owner. A nil Object means the owner is unchanged.
type ownerChange struct {
	Object catalog.Descriptor
	Target authorization.PrincipalRef
}

// planTypeOwnership decides ALTER TYPE or ALTER DOMAIN ... OWNER TO name.
func planTypeOwnership(h role.Host, auth ddl.Authority, s *parser.AlterOwner, name string, identity bool) (ownerChange, error) {
	view := h.AccessView(auth)
	path, err := h.SearchNamespaces(auth, s.Name, ids.TypeClassID)
	if err != nil {
		return ownerChange{}, err
	}
	var old *catalog.Type
	for _, namespace := range path {
		a := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: s.Name.Parts[len(s.Name.Parts)-1].Text})
		if a != (ids.ObjectAddress{}) {
			old = view.Catalog.MustType(a)
			break
		}
	}
	if old == nil {
		return ownerChange{}, sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, catalog.ObjectNameText(s.Name)))
	}
	diagnosticPath, _ := auth.ReferencePath()
	display := catalog.TypeDiagnosticNameAt(view.Catalog, old, diagnosticPath)
	if s.Kind == "domain" && !old.SQLType.IsDomain() {
		return ownerChange{}, sqlError("42809", display+" is not a domain")
	}
	if old.SQLType.IsArray() {
		element := view.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.SQLType.Element().ID())})
		elementName := catalog.TypeDiagnosticNameAt(view.Catalog, element, diagnosticPath)
		return ownerChange{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot alter array type " + display, Hint: "You can alter type " + elementName + ", which will alter the array type as well."}
	}
	if old.OwningRelation != (ids.ObjectAddress{}) {
		r := view.Catalog.MustRelation(old.OwningRelation)
		if r.RelationKind != catalog.CompositeRelation {
			return ownerChange{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: display + " is a table's row type", Hint: "Use ALTER TABLE instead."}
		}
	}
	target, err := access.SchemaOwner(view, name, identity)
	if err != nil {
		return ownerChange{}, err
	}
	if !old.SQLType.IsUserDefined() {
		return ownerChange{}, sqlError("0A000", "ownership of builtin types is unsupported")
	}
	if target == old.Owner {
		return ownerChange{}, nil
	}
	if err = view.Check(auth, access.Requirement{Object: view.Ref(old.Address()), OwnerOnly: true, OwnerNoun: "type"}); err != nil {
		return ownerChange{}, err
	}
	if err = access.OwnershipEligibility(view, auth, target, old.Namespace()); err != nil {
		return ownerChange{}, err
	}
	return ownerChange{Object: old, Target: target}, nil
}

// planDatabaseOwnership decides ALTER DATABASE ... OWNER TO name.
func planDatabaseOwnership(view access.View, env access.Environment, s *parser.AlterOwner, name string, identity bool) (ownerChange, error) {
	target, err := access.SchemaOwner(view, name, identity)
	if err != nil {
		return ownerChange{}, err
	}
	database := view.Catalog.MustDatabase(catalog.CurrentDatabaseAddress)
	if len(s.Name.Parts) != 1 || s.Name.Parts[0].Text != database.Name() {
		return ownerChange{}, sqlError("3D000", fmt.Sprintf(`database "%s" does not exist`, s.Name.Parts[len(s.Name.Parts)-1].Text))
	}
	if target == database.Owner {
		return ownerChange{}, nil
	}
	if err = view.Check(env, access.Requirement{Object: view.Ref(database.Address()), OwnerOnly: true}); err != nil {
		return ownerChange{}, err
	}
	if err = access.OwnershipSuccessor(view, target); err != nil {
		return ownerChange{}, err
	}
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return ownerChange{}, err
	}
	if !actor.Superuser && !actor.CreateDB {
		return ownerChange{}, sqlError("42501", "permission denied to change owner of database")
	}
	return ownerChange{Object: database, Target: target}, nil
}

// planRelationOwnership decides ALTER TABLE, SEQUENCE, VIEW, MATERIALIZED
// VIEW or FOREIGN TABLE ... OWNER TO name for old, resolved as ref. The
// engine checks materialized view authority before it.
func planRelationOwnership(view access.View, env access.Environment, old *catalog.Relation, ref ids.ObjectRef, kind, name string, identity bool) (ownerChange, error) {
	if kind == "view" && old.RelationKind != catalog.ViewRelation {
		return ownerChange{}, sqlError("42809", fmt.Sprintf(`"%s" is not a %s`, old.Name(), kind))
	}
	if old.RelationKind == catalog.CatalogRelation || old.RelationKind == catalog.IndexRelation || old.RelationKind == catalog.CompositeRelation {
		return ownerChange{}, analyze.TableKindError(old, "change")
	}
	if kind != "materialized view" {
		if err := view.Check(env, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return ownerChange{}, err
		}
	}
	target, err := access.SchemaOwner(view, name, identity)
	if err != nil {
		return ownerChange{}, err
	}
	if target == old.Owner {
		return ownerChange{}, nil
	}
	if old.Sequence != nil && old.Sequence.Owned != (ids.ObjectAddress{}) {
		parent := old.Sequence.Owned
		parent.SubID = 0
		return ownerChange{}, sequence.LinkedError(old, view.Catalog.MustRelation(parent), false)
	}
	if err = access.OwnershipEligibility(view, env, target, old.Namespace()); err != nil {
		return ownerChange{}, err
	}
	return ownerChange{Object: old, Target: target}, nil
}
