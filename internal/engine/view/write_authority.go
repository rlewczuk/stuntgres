package view

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// CheckWrite checks a write through an auto-updatable view. Caller rights
// are checked on the original logical target through binding, which also
// checks its query sources; a and env check each layer. Each nested layer
// uses its current owner or the original invoker, never the outer owner.
func CheckWrite(binding analyze.Privileges, a access.View, env RelationAdmission, b analyze.Binding, stmt parser.Statement) error {
	w := ViewWriteOf(b.ViewWrite)
	if err := analyze.CheckBinding(binding, w.Logical, stmt); err != nil {
		return err
	}
	invoker := a.State.CurrentUser
	check := func(authority access.View, req analyze.Requirement) error {
		return authority.Check(env, req.AccessRequirement())
	}
	for _, layer := range w.Layers {
		current, ok := a.Catalog.Relation(layer.Ref.Address)
		if !ok || current.View != layer.Relation.View {
			return ids.ErrCatalogReplaced
		}
		authority := a
		authority.State.CurrentUser = current.Owner
		if stored.ViewOf(current).Invoker() {
			authority.State.CurrentUser = invoker
		}
		source, ok := a.Catalog.Relation(layer.SourceRef.Address)
		if !ok || !index.SameRelationRowShape(source, layer.Source) {
			return ids.ErrCatalogReplaced
		}
		if err := env.AdmitRelation(source); err != nil {
			return err
		}
		if err := check(authority, WriteTargetRequirement(stmt, layer, source)); err != nil {
			return err
		}
		for occurrence := range stored.ViewOf(current).Sources() {
			leaf, included, err := WriteSourceRequirementLeaf(a, b, current, layer, occurrence)
			if err != nil {
				return err
			}
			if !included {
				continue
			}
			if err := env.AdmitRelation(leaf.Relation); err != nil {
				return err
			}
			visit := func(req analyze.Requirement) error { return check(authority, req) }
			if err := analyze.SourceReadRequirements(&leaf, visit); err != nil {
				return err
			}
			if occurrence == layer.MainOccurrence {
				continue
			}
			if err := analyze.SourceRowMarkRequirements(&leaf, visit); err != nil {
				return err
			}
			if leaf.Relation.RelationKind == catalog.ViewRelation {
				if err := CheckRowMarks(authority, env, leaf.Relation, leaf.Mark, invoker); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
