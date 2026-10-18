package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/ids"
)

type partitionConstraintReplay struct {
	original  CatalogRead
	temporary map[ids.ObjectAddress]string
	occupied  map[ids.ObjectAddress]map[string]bool
}

// Reserve names against the entire captured namespace, not just objects that
// happen to have been emitted. Temporary names depend on logical order, not OIDs.
func partitionConstraintDumpCatalog(view CatalogRead) (CatalogRead, *partitionConstraintReplay) {
	replay := &partitionConstraintReplay{original: view, temporary: make(map[ids.ObjectAddress]string), occupied: make(map[ids.ObjectAddress]map[string]bool)}
	var tables []*relationDescriptor
	reserved := make(map[ids.ObjectAddress]map[string]bool)
	for key := range view.Names() {
		if key.ClassID != ids.RelationClassID {
			continue
		}
		if reserved[key.Namespace] == nil {
			reserved[key.Namespace] = make(map[string]bool)
		}
		reserved[key.Namespace][key.Name] = true
	}
	for _, descriptor := range view.Descriptors() {
		table, ok := descriptor.(*relationDescriptor)
		if ok && hierarchyRelation(table) && (len(table.UniqueConstraints) != 0 || len(table.ExclusionConstraints) != 0) {
			tables = append(tables, table)
		}
	}
	slices.SortFunc(tables, func(a, b *relationDescriptor) int {
		return partitionReplayNameCompare(view, a, b)
	})
	if len(tables) == 0 {
		return view, replay
	}
	state := *view.state
	state.cloneDescriptors()
	ordinal := 0
	for _, table := range tables {
		// PARTITION OF binds local declarations before cloning inherited keys,
		// even when the captured table added its locals after inheritance. Name
		// allocation must not depend on that mutable slice order (or on OIDs).
		indexes := make([]*relationDescriptor, 0, len(table.UniqueConstraints)+len(table.ExclusionConstraints))
		for _, c := range table.UniqueConstraints {
			indexes = append(indexes, view.MustRelation(c.Index))
		}
		for _, c := range table.ExclusionConstraints {
			indexes = append(indexes, view.MustRelation(c.Index))
		}
		slices.SortFunc(indexes, func(a, b *relationDescriptor) int {
			return partitionReplayNameCompare(view, a, b)
		})
		for _, index := range indexes {
			for {
				ordinal++
				name := fmt.Sprintf("__stuntgres_partition_key_%d", ordinal)
				if !reserved[table.namespace][name] {
					reserved[table.namespace][name] = true
					replay.temporary[index.address] = name
					break
				}
			}
		}
		next := *table
		next.UniqueConstraints = slices.Clone(table.UniqueConstraints)
		for i := range next.UniqueConstraints {
			c := &next.UniqueConstraints[i]
			c.Name = replay.temporary[c.Index]
			old := view.MustRelation(c.Index)
			index, definition := *old, *old.SQLIndex
			index.name = c.Name
			definition.Constraint = uniqueBacking(c)
			index.SQLIndex = &definition
			state.setDescriptor(index.address, &index)
		}
		next.ExclusionConstraints = slices.Clone(table.ExclusionConstraints)
		for i := range next.ExclusionConstraints {
			c := &next.ExclusionConstraints[i]
			c.Name = replay.temporary[c.Index]
			old := view.MustRelation(c.Index)
			index, definition := *old, *old.SQLIndex
			index.name = c.Name
			definition.Constraint = exclusionBacking(c)
			index.SQLIndex = &definition
			state.setDescriptor(index.address, &index)
		}
		state.setDescriptor(next.address, &next)
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
	for _, c := range table.NotNullConstraints {
		used[c.Name] = true
	}
	for _, c := range table.CheckConstraints {
		used[c.Name] = true
	}
	for _, index := range partitionIndexDeclarationOrder(view, table) {
		if index.SQLIndex.Constraint != nil && index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) && !partitionReplaySeparateConstraint(view, index) {
			used[index.name] = true
		}
	}
	if table.Membership == nil || partitionDumpNeedsAttach(view, table) {
		return "", nil
	}
	return r.membershipRenames(view, table, dumpEventKey{oid: table.address.ObjectID}), nil
}

// A populated ATTACH can create an entire inherited subtree in one SQL event.
// Reserve that complete creation sequence before freeing any generated name.
func (r *partitionConstraintReplay) membershipRenames(view CatalogRead, table *relationDescriptor, event dumpEventKey) string {
	type rename struct {
		index     *relationDescriptor
		generated string
	}
	var renames []rename
	var reserve func(*relationDescriptor, *relationDescriptor)
	reserve = func(child, parentIndex *relationDescriptor) {
		index := partitionDumpIndexBacking(r.original, parentIndex.address, child.address)
		if index == nil || index.SQLIndex.Constraint == nil {
			return
		}
		if root := partitionReplayVirtualUniqueRoot(r.original, index); root != nil && !partitionReplayOnlyExclusion(root) {
			return
		}
		if partitionConstraintCreator(r.original, index) != event {
			return
		}
		used := r.occupied[index.namespace]
		if used == nil {
			used = make(map[string]bool)
			r.occupied[index.namespace] = used
		}
		suffix, addition := "key", ""
		if index.SQLIndex.isExclusion() {
			suffix = "excl"
		}
		if index.SQLIndex.Constraint.Primary {
			suffix = "pkey"
		} else {
			labels := make([]string, len(index.SQLIndex.Keys))
			for i := range labels {
				labels[i] = index.Columns[i].Name
			}
			addition = strings.Join(labels, "_")
		}
		name := generatedConstraintName(child.name, addition, suffix, used)
		used[name] = true
		renames = append(renames, rename{index, name})
		if event.phase == dumpTableAttachment && child.Partition != nil {
			for _, address := range child.Partition.Children {
				reserve(r.original.MustRelation(address), index)
			}
		}
	}
	parent := r.original.MustRelation(table.Membership.Parent)
	for _, index := range partitionIndexDeclarationOrder(r.original, parent) {
		reserve(table, index)
	}
	var sql strings.Builder
	for _, rename := range renames {
		target := r.temporary[rename.index.address]
		schema := quoteIdentifier(view.Get(rename.index.namespace).Name())
		sql.WriteString("ALTER INDEX " + schema + "." + quoteIdentifier(rename.generated) + " RENAME TO " + quoteIdentifier(target) + ";\n")
		used := r.occupied[rename.index.namespace]
		delete(used, rename.generated)
		used[target] = true
	}
	return sql.String()
}

func (r *partitionConstraintReplay) finalRenameOrder() []*relationDescriptor {
	var indexes []*relationDescriptor
	for address := range r.temporary {
		indexes = append(indexes, r.original.MustRelation(address))
	}
	slices.SortFunc(indexes, func(a, b *relationDescriptor) int {
		return partitionReplayNameCompare(r.original, a, b)
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
	schema := quoteIdentifier(r.original.Get(index.namespace).Name())
	return "ALTER INDEX " + schema + "." + quoteIdentifier(r.temporary[index.address]) + " RENAME TO " + quoteIdentifier(index.name) + ";\n"
}

func partitionIndexDumpAttachmentSQL(view CatalogRead, index *relationDescriptor) string {
	parent := view.MustRelation(index.SQLIndex.PartitionParent)
	return "ALTER INDEX " + quoteIdentifier(view.Get(parent.namespace).Name()) + "." + quoteIdentifier(parent.name) + " ATTACH PARTITION " + quoteIdentifier(view.Get(index.namespace).Name()) + "." + quoteIdentifier(index.name) + ";\n"
}
