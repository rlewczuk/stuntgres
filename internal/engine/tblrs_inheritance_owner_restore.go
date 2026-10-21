package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// CaptureInheritedOwner pairs owner, the role a named owner directive
// captured, with created, a permanent ordinary inheritance member the
// current superuser just created; the next statement must hand it over.
func CaptureInheritedOwner(view access.View, created ids.ObjectRef, owner authorization.PrincipalRef) (table.RestoredInheritedOwner, error) {
	reject := func() (table.RestoredInheritedOwner, error) {
		return table.RestoredInheritedOwner{}, sqlError("0A000", table.RestoreInheritedOwnerBoundary)
	}
	tbl, ok := view.Catalog.Relation(created.Address)
	if !ok || tbl.Inheritance == nil || tbl.RelationKind != catalog.TableRelation {
		return reject()
	}
	namespace, ok := view.Catalog.Namespace(tbl.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) || table.ValidateInheritance(view.Catalog, tbl) != nil {
		return reject()
	}
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return table.RestoredInheritedOwner{}, err
	}
	if !actor.Superuser || tbl.Owner != actor.Ref {
		return reject()
	}
	if _, err := view.Roles.Descriptor(owner); err != nil {
		return table.RestoredInheritedOwner{}, err
	}
	return table.RestoredInheritedOwner{Ref: created, Owner: owner, Creator: actor.Ref}, nil
}

// InheritedOwnerShape reports whether raw is the plain ALTER TABLE
// schema.table OWNER TO role that pair expects.
func InheritedOwnerShape(raw *parser.AlterOwner, pair table.RestoredInheritedOwner) bool {
	return raw.Kind == "table" && !raw.IfExists && len(raw.Name.Parts) == 2 && raw.Target.Identity == "" && pair.Ref != (ids.ObjectRef{})
}

// InheritedOwnerDirective checks that pair's creator, a superuser, is the
// current user and that raw hands the relation to pair's captured owner.
func InheritedOwnerDirective(view access.View, raw *parser.AlterOwner, pair table.RestoredInheritedOwner) error {
	reject := func() error { return sqlError("0A000", table.RestoreInheritedOwnerBoundary) }
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser || actor.Ref != pair.Creator {
		return reject()
	}
	role, err := view.Roles.Descriptor(pair.Owner)
	if err != nil {
		return err
	}
	if raw.Target.Name != role.Name {
		return reject()
	}
	return nil
}

// InheritedOwnerTarget checks that ref, the relation raw names, is pair's
// permanent inheritance member, still owned by its creator.
func InheritedOwnerTarget(view catalog.Read, ref ids.ObjectRef, pair table.RestoredInheritedOwner) error {
	reject := func() error { return sqlError("0A000", table.RestoreInheritedOwnerBoundary) }
	if ref != pair.Ref {
		return reject()
	}
	tbl, ok := view.Relation(ref.Address)
	if !ok || tbl.Inheritance == nil || tbl.Owner != pair.Creator {
		return reject()
	}
	namespace, ok := view.Namespace(tbl.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) || table.ValidateInheritance(view, tbl) != nil {
		return reject()
	}
	return nil
}
