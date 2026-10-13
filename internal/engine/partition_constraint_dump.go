package engine

import (
	"fmt"
	"slices"
	"strings"
)

type partitionConstraintReplay struct {
	original  CatalogRead
	temporary map[ObjectAddress]string
	occupied  map[ObjectAddress]map[string]bool
}

// Reserve names against the entire captured namespace, not just objects that
// happen to have been emitted. Temporary names depend on logical order, not OIDs.
func partitionConstraintDumpCatalog(view CatalogRead) (CatalogRead, *partitionConstraintReplay) {
	replay := &partitionConstraintReplay{original: view, temporary: make(map[ObjectAddress]string), occupied: make(map[ObjectAddress]map[string]bool)}
	var tables []*relationDescriptor
	reserved := make(map[ObjectAddress]map[string]bool)
	for key := range view.state.names {
		if key.ClassID != RelationClassID {
			continue
		}
		if reserved[key.Namespace] == nil {
			reserved[key.Namespace] = make(map[string]bool)
		}
		reserved[key.Namespace][key.Name] = true
	}
	for _, descriptor := range view.state.descriptors {
		table, ok := descriptor.(*relationDescriptor)
		if ok && hierarchyRelation(table) && len(table.uniqueConstraints) != 0 {
			tables = append(tables, table)
		}
	}
	slices.SortFunc(tables, func(a, b *relationDescriptor) int {
		left := view.state.descriptors[a.namespace].Name() + "." + a.name
		right := view.state.descriptors[b.namespace].Name() + "." + b.name
		return strings.Compare(left, right)
	})
	if len(tables) == 0 {
		return view, replay
	}
	state := *view.state
	state.descriptors = make(map[ObjectAddress]Descriptor, len(view.state.descriptors))
	for a, d := range view.state.descriptors {
		state.descriptors[a] = d
	}
	ordinal := 0
	for _, table := range tables {
		next := *table
		next.uniqueConstraints = slices.Clone(table.uniqueConstraints)
		for i := range next.uniqueConstraints {
			c := &next.uniqueConstraints[i]
			for {
				ordinal++
				name := fmt.Sprintf("__stuntgres_partition_key_%d", ordinal)
				if !reserved[table.namespace][name] {
					reserved[table.namespace][name] = true
					c.Name = name
					break
				}
			}
			replay.temporary[c.Index] = c.Name
			old := view.state.descriptors[c.Index].(*relationDescriptor)
			index, definition := *old, *old.sqlIndex
			index.name = c.Name
			definition.constraint = uniqueBacking(c)
			index.sqlIndex = &definition
			state.descriptors[index.address] = &index
		}
		state.descriptors[next.address] = &next
	}
	view.state = &state
	return view, replay
}

func (r *partitionConstraintReplay) tableRenames(view CatalogRead, table *relationDescriptor) (string, error) {
	used := r.occupied[table.namespace]
	if used == nil {
		used = make(map[string]bool)
		r.occupied[table.namespace] = used
	}
	used[table.name] = true
	for _, c := range table.notNullConstraints {
		used[c.Name] = true
	}
	for _, c := range table.checkConstraints {
		used[c.Name] = true
	}
	for _, c := range table.uniqueConstraints {
		if c.Parent == 0 {
			used[c.Name] = true
		}
	}
	if table.membership == nil || partitionDumpNeedsAttach(view, table) {
		return "", nil
	}
	parent := r.original.state.descriptors[table.membership.parent].(*relationDescriptor)
	type rename struct {
		constraint uniqueConstraint
		generated  string
	}
	var renames []rename
	for _, index := range partitionIndexDeclarationOrder(r.original, parent) {
		backing := index.sqlIndex.constraint
		if backing == nil || backing.kind != uniqueBackingKind {
			continue
		}
		for _, c := range table.uniqueConstraints {
			if c.Parent == backing.OID {
				renames = append(renames, rename{constraint: c})
				break
			}
		}
	}
	// Reserve the complete creation sequence before any RENAME frees a stem.
	for i := range renames {
		c := renames[i].constraint
		suffix, addition := "key", ""
		if c.Primary {
			suffix = "pkey"
		} else {
			labels := make([]string, len(c.Attributes))
			for i, a := range c.Attributes {
				labels[i] = table.columns[table.indexForAttribute(a)].Name
			}
			addition = strings.Join(labels, "_")
		}
		renames[i].generated = generatedConstraintName(table.name, addition, suffix, used)
		used[renames[i].generated] = true
	}
	var sql strings.Builder
	schema := quoteIdentifier(view.state.descriptors[table.namespace].Name())
	for _, rename := range renames {
		target := r.temporary[rename.constraint.Index]
		sql.WriteString("ALTER INDEX " + schema + "." + quoteIdentifier(rename.generated) + " RENAME TO " + quoteIdentifier(target) + ";\n")
		delete(used, rename.generated)
		used[target] = true
	}
	return sql.String(), nil
}

func (r *partitionConstraintReplay) finalRenameOrder() []*relationDescriptor {
	var indexes []*relationDescriptor
	for address := range r.temporary {
		indexes = append(indexes, r.original.state.descriptors[address].(*relationDescriptor))
	}
	slices.SortFunc(indexes, func(a, b *relationDescriptor) int {
		return strings.Compare(r.original.state.descriptors[a.namespace].Name()+"."+a.name, r.original.state.descriptors[b.namespace].Name()+"."+b.name)
	})
	return indexes
}

func (r *partitionConstraintReplay) finalRenameSQL(index *relationDescriptor) string {
	used := r.occupied[index.namespace]
	if used == nil {
		used = make(map[string]bool)
		r.occupied[index.namespace] = used
	}
	delete(used, r.temporary[index.address])
	used[index.name] = true
	schema := quoteIdentifier(r.original.state.descriptors[index.namespace].Name())
	return "ALTER INDEX " + schema + "." + quoteIdentifier(r.temporary[index.address]) + " RENAME TO " + quoteIdentifier(index.name) + ";\n"
}

func partitionIndexDumpAttachmentSQL(view CatalogRead, index *relationDescriptor) string {
	parent := view.state.descriptors[index.sqlIndex.partitionParent].(*relationDescriptor)
	return "ALTER INDEX " + quoteIdentifier(view.state.descriptors[parent.namespace].Name()) + "." + quoteIdentifier(parent.name) + " ATTACH PARTITION " + quoteIdentifier(view.state.descriptors[index.namespace].Name()) + "." + quoteIdentifier(index.name) + ";\n"
}
