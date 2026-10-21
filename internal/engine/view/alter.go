package view

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// PostgreSQL uses different callbacks for column, dependency and other forms.
// Keep admission and execution on the same action-specific authority contract.
func AlterViewAuthority(view access.View, env access.Environment, ref ids.ObjectRef, s *parser.AlterView) error {
	d := view.Catalog.MustRelation(ref.Address)
	kind, label := catalog.ViewRelation, "view"
	ownerChecked := false
	if s.Materialized {
		kind, label = catalog.MaterializedViewRelation, "materialized view"
		if s.Action == "column" {
			return view.Check(env, access.Requirement{Object: ref, OwnerOnly: true})
		}
		if s.Action != "depends" && s.Action != "no_depends" {
			if err := view.Check(env, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
				return err
			}
			ownerChecked = true
		}
	}
	if d.RelationKind != kind {
		return sqlError("42809", fmt.Sprintf(`"%s" is not a %s`, d.Name(), label))
	}
	if ownerChecked {
		return nil
	}
	return view.Check(env, access.Requirement{Object: ref, OwnerOnly: true})
}

func ViewDropOwner(view access.View, env access.Environment, ref ids.ObjectRef, d *catalog.Relation) error {
	ownerErr := view.Check(env, access.Requirement{Object: ref, OwnerOnly: true})
	if ownerErr == nil {
		return nil
	}
	ns := view.Catalog.MustNamespace(d.Namespace())
	allowed, err := view.Roles.InheritsRole(view.State.CurrentUser, ns.Owner)
	if err != nil {
		return err
	}
	if allowed {
		return nil
	}
	return ownerErr
}

func ViewDropMissing(err error, name parser.ObjectName, label string) error {
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "42P01" {
		return err
	}
	return sqlError("42P01", fmt.Sprintf(`%s "%s" does not exist`, label, name.Parts[len(name.Parts)-1].Text))
}
