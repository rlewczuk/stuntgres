package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

type partitionConstraintReplay struct {
	original  catalog.Read
	resolver  dump.Resolver
	temporary map[ids.ObjectAddress]string
	occupied  map[ids.ObjectAddress]map[string]bool
}

// Reserve names against the entire captured namespace, not just objects that
// happen to have been emitted. Temporary names depend on logical order, not OIDs.
func partitionConstraintDumpCatalog(r dump.Resolver, view catalog.Read) (catalog.Read, *partitionConstraintReplay) {
	replay := &partitionConstraintReplay{original: view, resolver: r, temporary: make(map[ids.ObjectAddress]string), occupied: make(map[ids.ObjectAddress]map[string]bool)}
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
		if ok && catalog.HierarchyRelation(table) && (len(table.Constraints.Unique) != 0 || len(table.Constraints.Exclusion) != 0) {
			tables = append(tables, table)
		}
	}
	slices.SortFunc(tables, func(a, b *catalog.Relation) int {
		return index.PartitionReplayNameCompare(view, a, b)
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
			return index.PartitionReplayNameCompare(view, a, b)
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
			idx, definition := *old, *old.SQLIndex
			idx.ObjectName = c.Name
			definition.Constraint = index.UniqueBacking(c)
			idx.SQLIndex = &definition
			state.SetDescriptor(idx.Address(), &idx)
		}
		next.Constraints.Exclusion = slices.Clone(table.Constraints.Exclusion)
		for i := range next.Constraints.Exclusion {
			c := &next.Constraints.Exclusion[i]
			c.Name = replay.temporary[c.Index]
			old := view.MustRelation(c.Index)
			idx, definition := *old, *old.SQLIndex
			idx.ObjectName = c.Name
			definition.Constraint = index.ExclusionBacking(c)
			idx.SQLIndex = &definition
			state.SetDescriptor(idx.Address(), &idx)
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
	for _, idx := range index.PartitionIndexDeclarationOrder(view, table) {
		if idx.SQLIndex.Constraint != nil && idx.SQLIndex.PartitionParent == (ids.ObjectAddress{}) && !index.PartitionReplaySeparateConstraint(view, idx) {
			used[idx.Name()] = true
		}
	}
	if table.Membership == nil || partitionDumpNeedsAttach(view, table) {
		return "", nil
	}
	return r.membershipRenames(view, table, dump.Event{OID: table.Address().ObjectID}), nil
}

// A populated ATTACH can create an entire inherited subtree in one SQL event.
// Reserve that complete creation sequence before freeing any generated name.
func (r *partitionConstraintReplay) membershipRenames(view catalog.Read, table *catalog.Relation, event dump.Event) string {
	type rename struct {
		index     *catalog.Relation
		generated string
	}
	var renames []rename
	var reserve func(*catalog.Relation, *catalog.Relation)
	reserve = func(child, parentIndex *catalog.Relation) {
		idx := index.PartitionDumpIndexBacking(r.original, parentIndex.Address(), child.Address())
		if idx == nil || idx.SQLIndex.Constraint == nil {
			return
		}
		if root := index.PartitionReplayVirtualUniqueRoot(r.original, idx); root != nil && !index.PartitionReplayOnlyExclusion(root) {
			return
		}
		if partitionConstraintCreator(r.resolver, r.original, idx) != event {
			return
		}
		used := r.occupied[idx.Namespace()]
		if used == nil {
			used = make(map[string]bool)
			r.occupied[idx.Namespace()] = used
		}
		suffix, addition := "key", ""
		if idx.SQLIndex.IsExclusion() {
			suffix = "excl"
		}
		if idx.SQLIndex.Constraint.Primary {
			suffix = "pkey"
		} else {
			labels := make([]string, len(idx.SQLIndex.Keys))
			for i := range labels {
				labels[i] = idx.Columns[i].Name
			}
			addition = strings.Join(labels, "_")
		}
		name := catalog.GeneratedConstraintName(child.Name(), addition, suffix, used)
		used[name] = true
		renames = append(renames, rename{idx, name})
		if event.Phase == dump.TableAttachment && child.Partition != nil {
			for _, address := range child.Partition.Children {
				reserve(r.original.MustRelation(address), idx)
			}
		}
	}
	parent := r.original.MustRelation(table.Membership.Parent)
	for _, index := range index.PartitionIndexDeclarationOrder(r.original, parent) {
		reserve(table, index)
	}
	var sql strings.Builder
	for _, rename := range renames {
		target := r.temporary[rename.index.Address()]
		schema := catalog.QuoteIdentifier(view.Get(rename.index.Namespace()).Name())
		sql.WriteString("ALTER INDEX " + schema + "." + catalog.QuoteIdentifier(rename.generated) + " RENAME TO " + catalog.QuoteIdentifier(target) + ";\n")
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
		return index.PartitionReplayNameCompare(r.original, a, b)
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
	schema := catalog.QuoteIdentifier(r.original.Get(index.Namespace()).Name())
	return "ALTER INDEX " + schema + "." + catalog.QuoteIdentifier(r.temporary[index.Address()]) + " RENAME TO " + catalog.QuoteIdentifier(index.Name()) + ";\n"
}

func partitionIndexDumpAttachmentSQL(view catalog.Read, index *catalog.Relation) string {
	parent := view.MustRelation(index.SQLIndex.PartitionParent)
	return "ALTER INDEX " + catalog.QuoteIdentifier(view.Get(parent.Namespace()).Name()) + "." + catalog.QuoteIdentifier(parent.Name()) + " ATTACH PARTITION " + catalog.QuoteIdentifier(view.Get(index.Namespace()).Name()) + "." + catalog.QuoteIdentifier(index.Name()) + ";\n"
}
