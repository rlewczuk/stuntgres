package table

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// The ordinary dependency closure owns discovery/order/RESTRICT diagnostics.
// This pass consumes only FK definition edges, never the referencing table/data.
func ForeignKeyDependents(provider DropDependencyReader, edges []DomainDependency) error {
	for _, edge := range edges {
		if edge.Address.ClassID != 2606 || edge.Relation == nil {
			continue
		}
		if provider.Removed(edge.Relation.Address()) {
			continue
		}
		old := edge.Relation
		if replacement, ok := provider.Replacement(old.Address()).(*catalog.Relation); ok {
			old = replacement
		}
		for i, f := range old.Constraints.ForeignKeys {
			if f.OID != edge.Address.ObjectID {
				continue
			}
			next := *old
			next.Constraints.ForeignKeys = make([]catalog.ForeignKeyConstraint, 0, len(old.Constraints.ForeignKeys)-1)
			next.Constraints.ForeignKeys = append(next.Constraints.ForeignKeys, old.Constraints.ForeignKeys[:i]...)
			next.Constraints.ForeignKeys = append(next.Constraints.ForeignKeys, old.Constraints.ForeignKeys[i+1:]...)
			var err error
			next.ObjectRevision, err = provider.Revision(old.Revision())
			if err != nil {
				return err
			}
			if err := provider.Replace(&next); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// RebuildAlterForeignKeys rebinds the foreign keys whose referencing or
// referenced columns a TYPE transformation of parent changed, with fresh
// identities.
func RebuildAlterForeignKeys(h ddl.Host, parent *catalog.Relation, transformations []index.ColumnTransformation) (*catalog.Relation, error) {
	if len(transformations) == 0 {
		return parent, nil
	}
	view := h.StageRead()
	for _, descriptor := range view.Descriptors() {
		old, ok := descriptor.(*catalog.Relation)
		if !ok || len(old.Constraints.ForeignKeys) == 0 {
			continue
		}
		var next *catalog.Relation
		for i, f := range old.Constraints.ForeignKeys {
			affected := false
			for _, change := range transformations {
				affected = affected || (old.Address() == parent.Address() && slices.Contains(f.Attributes, change.Attribute)) || (f.Target == parent.Address() && slices.Contains(f.Referenced, change.Attribute))
			}
			if !affected {
				continue
			}
			if next == nil {
				copy := *old
				copy.Constraints.ForeignKeys = slices.Clone(old.Constraints.ForeignKeys)
				next = &copy
				if old.Address() == parent.Address() {
					*next = *parent
					next.Constraints.ForeignKeys = slices.Clone(parent.Constraints.ForeignKeys)
				}
			}
			c := &next.Constraints.ForeignKeys[i]
			target, ok := view.Relation(c.Target)
			if c.Target == parent.Address() {
				target = parent
				ok = true
			}
			if !ok {
				return nil, ids.ErrCatalogReplaced
			}
			index, err := ForeignKeyIndex(view, target, c.Referenced)
			if err != nil {
				return nil, err
			}
			c.Index = index.Address()
			if _, err := BindForeignKeyComparison(view, next, target, index, *c); err != nil {
				return nil, err
			}
			c.OID, err = h.AllocateOID()
			if err != nil {
				return nil, err
			}
		}
		if next != nil {
			var err error
			next.ObjectRevision, err = h.Revision()
			if err != nil {
				return nil, err
			}
			if err := h.Stage().Replace(next); err != nil {
				return nil, err
			}
			if next.Address() == parent.Address() {
				parent = next
			}
		}
	}
	return parent, nil
}
