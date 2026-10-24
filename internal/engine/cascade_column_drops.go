package engine

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// cascadeColumnDrops runs the column, domain-CHECK and retired-provider steps
// of a cascading drop, in r's order. Each surviving relation of relations
// loses the columns edges name, and project projects its new shape. Then each
// edge's domain CHECK goes, and the retired providers among targets are
// removed.
func cascadeColumnDrops(r table.DropDependencyReader, relations []*catalog.Relation, edges []table.DomainDependency, targets []ids.ObjectAddress, project func(old, next *catalog.Relation) error) error {
	var err error
	for _, old := range relations {
		if r.Removed(old.Address()) {
			continue
		}
		// Whole-relation targets need no standalone shape projection or
		// surviving-participant capability check.
		if slices.Contains(targets, old.Address()) {
			continue
		}
		ref := r.BaseRef(old.Address())
		next := old
		consumed := false
		for _, edge := range edges {
			if edge.Column == "" || edge.Relation.Address() != old.Address() {
				continue
			}
			if r.Removed(old.Address()) {
				break
			}
			if replacement, ok := r.Replacement(old.Address()).(*catalog.Relation); ok {
				next = replacement
			}
			// The closure captured a live stable attribute. A nested cascade
			// may already have consumed it through a different provider path.
			slot := next.IndexForAttribute(edge.Address.SubID)
			if slot < 0 {
				continue
			}
			if err = r.InheritanceAlterGuard(next); err != nil {
				return err
			}
			next, err = table.OrdinaryColumnDrop(r, next, ref, parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: next.Columns[slot].Name}, Mode: parser.DropCascade})
			if err != nil {
				return err
			}
			consumed = true
		}
		if !consumed {
			continue
		}
		if r.Removed(old.Address()) {
			continue
		}
		revision, er := r.Revision(old.Revision())
		if er != nil {
			return er
		}
		next.ObjectRevision = revision
		if err := project(old, next); err != nil {
			return err
		}
	}
	for _, edge := range edges {
		if edge.Domain == nil {
			continue
		}
		if r.Removed(edge.Domain.Address()) {
			continue
		}
		if err := table.RemoveDomainCheck(r, edge.Domain, edge.Address.ObjectID); err != nil {
			return err
		}
	}
	for _, target := range targets {
		if table.RetiredProviderClass(target.ClassID) {
			if err = r.Remove(r.BaseRef(target)); err != nil {
				break
			}
		}
	}
	if err != nil {
		return err
	}
	return nil
}
