package table

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

const RestoreInheritedOwnerBoundary = "restore accepts only immediately paired table declaration ownership"

type RestoredInheritedOwner struct {
	Ref     ids.ObjectRef
	Owner   authorization.PrincipalRef
	Creator authorization.PrincipalRef
}

// CaptureInheritedOwner pairs owner, the role a named owner directive
// captured, with created, a permanent ordinary table (including inheritance
// members) the current superuser created, for an immediately following transfer.
func CaptureInheritedOwner(view access.View, created ids.ObjectRef, owner authorization.PrincipalRef) (RestoredInheritedOwner, error) {
	reject := func() (RestoredInheritedOwner, error) {
		return RestoredInheritedOwner{}, sqlError("0A000", RestoreInheritedOwnerBoundary)
	}
	tbl, ok := view.Catalog.Relation(created.Address)
	if !ok || tbl.RelationKind != catalog.TableRelation || tbl.Partition != nil || tbl.Membership != nil {
		return reject()
	}
	namespace, ok := view.Catalog.Namespace(tbl.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) || ValidateInheritance(view.Catalog, tbl) != nil {
		return reject()
	}
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return RestoredInheritedOwner{}, err
	}
	if !actor.Superuser || tbl.Owner != actor.Ref {
		return reject()
	}
	if _, err := view.Roles.Descriptor(owner); err != nil {
		return RestoredInheritedOwner{}, err
	}
	return RestoredInheritedOwner{Ref: created, Owner: owner, Creator: actor.Ref}, nil
}

// InheritedOwnerShape reports whether raw is the plain ALTER TABLE
// schema.table OWNER TO role that pair expects.
func InheritedOwnerShape(raw *parser.AlterOwner, pair RestoredInheritedOwner) bool {
	return raw.Kind == "table" && !raw.IfExists && len(raw.Name.Parts) == 2 && raw.Target.Identity == "" && pair.Ref != (ids.ObjectRef{})
}

// InheritedOwnerDirective checks that pair's creator, a superuser, is the
// current user and that raw hands the relation to pair's captured owner.
func InheritedOwnerDirective(view access.View, raw *parser.AlterOwner, pair RestoredInheritedOwner) error {
	reject := func() error { return sqlError("0A000", RestoreInheritedOwnerBoundary) }
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
// permanent ordinary table, still owned by its creator.
func InheritedOwnerTarget(view catalog.Read, ref ids.ObjectRef, pair RestoredInheritedOwner) error {
	reject := func() error { return sqlError("0A000", RestoreInheritedOwnerBoundary) }
	if ref != pair.Ref {
		return reject()
	}
	tbl, ok := view.Relation(ref.Address)
	if !ok || tbl.RelationKind != catalog.TableRelation || tbl.Partition != nil || tbl.Membership != nil || tbl.Owner != pair.Creator {
		return reject()
	}
	namespace, ok := view.Namespace(tbl.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) || ValidateInheritance(view, tbl) != nil {
		return reject()
	}
	return nil
}
