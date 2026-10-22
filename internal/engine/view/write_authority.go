package view

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// CheckWrite checks a write through an auto-updatable view. Caller rights
// are checked on the original logical target through binding, which also
// checks its query sources; a and env check each layer. Each nested layer
// uses its current owner or the original invoker, never the outer owner.
func CheckWrite(binding analyze.Privileges, a access.View, env access.Environment, b analyze.Binding, stmt parser.Statement) error {
	w := ViewWriteOf(b.ViewWrite)
	logical := w.Logical
	if err := analyze.CheckBinding(binding, logical, stmt); err != nil {
		return err
	}
	invoker := a.State.CurrentUser
	mask := authorization.UpdatePrivilege
	switch stmt.(type) {
	case *parser.Insert:
		mask = authorization.InsertPrivilege
	case *parser.Delete:
		mask = authorization.DeletePrivilege
	}
	reads := b.Returning != nil || len(logical.UsedColumns) != 0 || logical.Insert.Conflict != nil && len(logical.Insert.Conflict.Reads) != 0
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
		if mask == authorization.DeletePrivilege {
			if err := authority.Check(env, access.Requirement{Object: layer.SourceRef, Privileges: mask}); err != nil {
				return err
			}
		} else if err := authority.CheckColumns(env, layer.SourceRef, source, mask, layer.Targets); err != nil {
			return err
		}
		for occurrence, leaf := range stored.ViewOf(current).Sources() {
			// Only the rewritten main target inherits outer command reads.
			// Independent subquery occurrences keep their own SELECT record,
			// including another occurrence of the same physical relation.
			if occurrence == layer.MainOccurrence && !reads {
				continue
			}
			relation, ok := a.Catalog.Relation(leaf.Ref.Address)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			use, err := stored.ViewUseReference(a.Catalog, current, leaf.Ref)
			if err != nil {
				return err
			}
			indices := make([]int, 0, len(leaf.Reads))
			for _, attr := range leaf.Reads {
				found := false
				for i, live := range relation.Attributes {
					if live == attr {
						indices = append(indices, i)
						found = true
						break
					}
				}
				if !found {
					return ids.ErrCatalogReplaced
				}
			}
			if err := authority.CheckColumns(env, use, relation, authorization.SelectPrivilege, indices); err != nil {
				return err
			}
			if occurrence == layer.MainOccurrence {
				continue // The next write layer owns main-source expansion.
			}
			mark := stored.ViewLeafRowMark(stored.ViewOf(current), occurrence, plan.RowMark{})
			if mark.Mode != 0 {
				if err := authority.CheckColumns(env, use, relation, authorization.UpdatePrivilege, nil); err != nil {
					return err
				}
			}
			// This existing visitor checks child definition SELECT records and
			// applicable row marks under each child's owner/original invoker.
			if relation.RelationKind == catalog.ViewRelation {
				if err := CheckRowMarks(authority, env, relation, mark, invoker); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
