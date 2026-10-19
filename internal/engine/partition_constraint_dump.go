package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

type partitionConstraintReplay struct {
	original  catalog.Read
	temporary map[ids.ObjectAddress]string
	occupied  map[ids.ObjectAddress]map[string]bool
}

// Reserve names against the entire captured namespace, not just objects that
// happen to have been emitted. Temporary names depend on logical order, not OIDs.
func partitionConstraintDumpCatalog(view catalog.Read) (catalog.Read, *partitionConstraintReplay) {
	replay := &partitionConstraintReplay{original: view, temporary: make(map[ids.ObjectAddress]string), occupied: make(map[ids.ObjectAddress]map[string]bool)}
	var tables []*catalog.Relation
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
		table, ok := descriptor.(*catalog.Relation)
		if ok && hierarchyRelation(table) && (len(table.Constraints.Unique) != 0 || len(table.Constraints.Exclusion) != 0) {
			tables = append(tables, table)
		}
	}
	slices.SortFunc(tables, func(a, b *catalog.Relation) int {
		return partitionReplayNameCompare(view, a, b)
	})
	if len(tables) == 0 {
		return view, replay
	}
	state := *view.State()
	state.CloneDescriptors()
	ordinal := 0
	for _, table := range tables {
		// PARTITION OF binds local declarations before cloning inherited keys,
		// even when the captured table added its locals after inheritance. Name
		// allocation must not depend on that mutable slice order (or on OIDs).
		indexes := make([]*catalog.Relation, 0, len(table.Constraints.Unique)+len(table.Constraints.Exclusion))
		for _, c := range table.Constraints.Unique {
			indexes = append(indexes, view.MustRelation(c.Index))
		}
		for _, c := range table.Constraints.Exclusion {
			indexes = append(indexes, view.MustRelation(c.Index))
		}
		slices.SortFunc(indexes, func(a, b *catalog.Relation) int {
			return partitionReplayNameCompare(view, a, b)
		})
		for _, index := range indexes {
			for {
				ordinal++
				name := fmt.Sprintf("__stuntgres_partition_key_%d", ordinal)
				if !reserved[table.Namespace()][name] {
					reserved[table.Namespace()][name] = true
					replay.temporary[index.Address()] = name
					break
				}
			}
		}
		next := *table
		next.Constraints.Unique = slices.Clone(table.Constraints.Unique)
		for i := range next.Constraints.Unique {
			c := &next.Constraints.Unique[i]
			c.Name = replay.temporary[c.Index]
			old := view.MustRelation(c.Index)
			index, definition := *old, *old.SQLIndex
			index.ObjectName = c.Name
			definition.Constraint = uniqueBacking(c)
			index.SQLIndex = &definition
			state.SetDescriptor(index.Address(), &index)
		}
		next.Constraints.Exclusion = slices.Clone(table.Constraints.Exclusion)
		for i := range next.Constraints.Exclusion {
			c := &next.Constraints.Exclusion[i]
			c.Name = replay.temporary[c.Index]
			old := view.MustRelation(c.Index)
			index, definition := *old, *old.SQLIndex
			index.ObjectName = c.Name
			definition.Constraint = exclusionBacking(c)
			index.SQLIndex = &definition
			state.SetDescriptor(index.Address(), &index)
		}
		state.SetDescriptor(next.Address(), &next)
	}
	view = view.WithState(&state)
	return view, replay
}

func (r *partitionConstraintReplay) tableRenames(view catalog.Read, table *catalog.Relation) (string, error) {
	used := r.occupied[table.Namespace()]
	if used == nil {
		used = make(map[string]bool)
		r.occupied[table.Namespace()] = used
	}
	used[table.Name()] = true
	for _, c := range table.Constraints.NotNull {
		used[c.Name] = true
	}
	for _, c := range table.Constraints.Check {
		used[c.Name] = true
	}
	for _, index := range partitionIndexDeclarationOrder(view, table) {
		if index.SQLIndex.Constraint != nil && index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) && !partitionReplaySeparateConstraint(view, index) {
			used[index.Name()] = true
		}
	}
	if table.Membership == nil || partitionDumpNeedsAttach(view, table) {
		return "", nil
	}
	return r.membershipRenames(view, table, dumpEventKey{oid: table.Address().ObjectID}), nil
}

// A populated ATTACH can create an entire inherited subtree in one SQL event.
// Reserve that complete creation sequence before freeing any generated name.
func (r *partitionConstraintReplay) membershipRenames(view catalog.Read, table *catalog.Relation, event dumpEventKey) string {
	type rename struct {
		index     *catalog.Relation
		generated string
	}
	var renames []rename
	var reserve func(*catalog.Relation, *catalog.Relation)
	reserve = func(child, parentIndex *catalog.Relation) {
		index := partitionDumpIndexBacking(r.original, parentIndex.Address(), child.Address())
		if index == nil || index.SQLIndex.Constraint == nil {
			return
		}
		if root := partitionReplayVirtualUniqueRoot(r.original, index); root != nil && !partitionReplayOnlyExclusion(root) {
			return
		}
		if partitionConstraintCreator(r.original, index) != event {
			return
		}
		used := r.occupied[index.Namespace()]
		if used == nil {
			used = make(map[string]bool)
			r.occupied[index.Namespace()] = used
		}
		suffix, addition := "key", ""
		if index.SQLIndex.IsExclusion() {
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
		name := generatedConstraintName(child.Name(), addition, suffix, used)
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
		target := r.temporary[rename.index.Address()]
		schema := quoteIdentifier(view.Get(rename.index.Namespace()).Name())
		sql.WriteString("ALTER INDEX " + schema + "." + quoteIdentifier(rename.generated) + " RENAME TO " + quoteIdentifier(target) + ";\n")
		used := r.occupied[rename.index.Namespace()]
		delete(used, rename.generated)
		used[target] = true
	}
	return sql.String()
}

func (r *partitionConstraintReplay) finalRenameOrder() []*catalog.Relation {
	var indexes []*catalog.Relation
	for address := range r.temporary {
		indexes = append(indexes, r.original.MustRelation(address))
	}
	slices.SortFunc(indexes, func(a, b *catalog.Relation) int {
		return partitionReplayNameCompare(r.original, a, b)
	})
	return indexes
}

func (r *partitionConstraintReplay) finalRenameSQL(index *catalog.Relation) string {
	used := r.occupied[index.Namespace()]
	if used == nil {
		used = make(map[string]bool)
		r.occupied[index.Namespace()] = used
	}
	delete(used, r.temporary[index.Address()])
	used[index.Name()] = true
	schema := quoteIdentifier(r.original.Get(index.Namespace()).Name())
	return "ALTER INDEX " + schema + "." + quoteIdentifier(r.temporary[index.Address()]) + " RENAME TO " + quoteIdentifier(index.Name()) + ";\n"
}

func partitionIndexDumpAttachmentSQL(view catalog.Read, index *catalog.Relation) string {
	parent := view.MustRelation(index.SQLIndex.PartitionParent)
	return "ALTER INDEX " + quoteIdentifier(view.Get(parent.Namespace()).Name()) + "." + quoteIdentifier(parent.Name()) + " ATTACH PARTITION " + quoteIdentifier(view.Get(index.Namespace()).Name()) + "." + quoteIdentifier(index.Name()) + ";\n"
}
