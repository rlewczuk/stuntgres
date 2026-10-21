package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Closure is physical-identity deduplication within one SQL occurrence. A foreign
// session's temporary branch is not admitted, even through a persistent ancestor.
func inheritanceQueryClosure(view catalog.Read, root *catalog.Relation, token ids.SessionToken, visit func(*catalog.Relation) error) error {
	if err := visit(root); err != nil {
		return err
	}
	children := catalog.InheritanceChildren(view, root.Address())
	if len(children) == 0 {
		return nil
	}
	seen := map[ids.ObjectAddress]bool{root.Address(): true}
	var walk func([]*catalog.Relation) error
	walk = func(children []*catalog.Relation) error {
		for _, child := range children {
			if seen[child.Address()] {
				continue
			}
			seen[child.Address()] = true
			ns, ok := view.Namespace(child.Namespace())
			if !ok {
				return ids.ErrCatalogReplaced
			}
			if ns.Temporary != (ids.SessionToken{}) && ns.Temporary != token {
				continue
			}
			if err := visit(child); err != nil {
				return err
			}
			if err := walk(catalog.InheritanceChildren(view, child.Address())); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(children)
}

func sourceOnly(statement parser.Statement) bool {
	if query, ok := statement.(*parser.Select); ok && query.From != nil {
		if table, ok := query.From.(*parser.TableReference); ok {
			return table.Only
		}
	}
	return false
}
