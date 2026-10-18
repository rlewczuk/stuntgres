package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Delayed virtual definitions own a local constraint origin. Recursive UNIQUE
// creates its tree at declaration; incomplete EXCLUDE uses ONLY and memberships.
func partitionReplayVirtualUniqueRoot(view catalog.Read, index *catalog.Relation) *catalog.Relation {
	if index.SQLIndex == nil || index.SQLIndex.Constraint == nil {
		return nil
	}
	for index.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		index = view.MustRelation(index.SQLIndex.PartitionParent)
		if index.SQLIndex.Constraint == nil {
			return nil
		}
	}
	table := view.MustRelation(index.SQLIndex.Parent)
	if table.Partition != nil && (partitionReplayOnlyExclusion(index) || partitionPreservedReplayLocal(view, index)) {
		return index
	}
	return nil
}

func partitionReplaySeparateConstraint(view catalog.Read, index *catalog.Relation) bool {
	return partitionReplayUsingIndex(view, index) || partitionReplayVirtualUniqueRoot(view, index) != nil
}

// ONLY declarations create just the local origin. Captured covered providers
// are created later by their table membership, never by index attachment.
func partitionReplayOnlyExclusion(index *catalog.Relation) bool {
	return index.SQLIndex != nil && index.SQLIndex.IsExclusion() && !index.SQLIndex.Valid && index.SQLIndex.PartitionParent == (ids.ObjectAddress{})
}

func partitionConstraintCreator(view catalog.Read, index *catalog.Relation) dumpEventKey {
	root := partitionReplayVirtualUniqueRoot(view, index)
	if root != nil && (!partitionReplayOnlyExclusion(root) || root.Address() == index.Address()) {
		return dumpEventKey{oid: root.Address().ObjectID}
	}
	// UNIQUE can adopt a provider declared on the standalone child before
	// ATTACH. Its name is an attachment prerequisite, not an attachment output.
	// Only EXCLUDE's mandatory fresh clones use membership creator readiness.
	if !index.SQLIndex.IsExclusion() || index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) || partitionReplayUsingIndex(view, index) {
		oid, _ := dumpPredecessor(view, index.Address(), ids.ObjectAddress{})
		return dumpEventKey{oid: oid}
	}
	table := view.MustRelation(index.SQLIndex.Parent)
	creator := dumpEventKey{oid: table.Address().ObjectID}
	for index.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		table = view.MustRelation(index.SQLIndex.Parent)
		if partitionDumpNeedsAttach(view, table) {
			creator = dumpEventKey{oid: table.Address().ObjectID, phase: dumpTableAttachment}
		}
		index = view.MustRelation(index.SQLIndex.PartitionParent)
	}
	return creator
}

// Remove only DEFAULT-last edges contradicted by an ONLY origin's captured
// gap-before-declaration-before-covered-membership requirements.
func partitionExclusionDefaultBefore(view catalog.Read, gap, sibling *catalog.Relation) bool {
	if gap.Membership == nil || !gap.Membership.DefaultBound {
		return false
	}
	parent := view.MustRelation(gap.Membership.Parent)
	for _, index := range partitionIndexDeclarationOrder(view, parent) {
		if partitionReplayOnlyExclusion(index) && partitionDumpIndexBacking(view, index.Address(), gap.Address()) == nil && partitionDumpIndexBacking(view, index.Address(), sibling.Address()) != nil {
			return true
		}
	}
	return false
}

func partitionVirtualUniqueDefinitionSQL(view catalog.Read, index *catalog.Relation) (string, error) {
	table := view.MustRelation(index.SQLIndex.Parent)
	if index.SQLIndex.IsExclusion() {
		text, err := indexDefinitionSQL(context.Background(), view, index, indexPresentation{dump: true, exclusion: true})
		if err != nil {
			return "", err
		}
		only := ""
		if partitionReplayOnlyExclusion(index) {
			only = "ONLY "
		}
		return "ALTER TABLE " + only + quoteIdentifier(view.Get(table.Namespace()).Name()) + "." + quoteIdentifier(table.Name()) + " ADD CONSTRAINT " + quoteIdentifier(index.Name()) + " " + text + ";\n", nil
	}
	for _, constraint := range table.UniqueConstraints {
		if constraint.Index != index.Address() {
			continue
		}
		text, err := (pgRenderer{view: view}).uniqueConstraintSQL(table, &constraint)
		if err != nil {
			return "", err
		}
		return "ALTER TABLE " + quoteIdentifier(view.Get(table.Namespace()).Name()) + "." + quoteIdentifier(table.Name()) + " ADD CONSTRAINT " + quoteIdentifier(constraint.Name) + " " + text + ";\n", nil
	}
	return "", fmt.Errorf("stuntgres: missing virtual unique replay definition")
}

// Reserve the real recursive creation sequence before any RENAME frees a stem,
// exactly like existing inline partition constraint replay. Final names remain
// owned by the scheduler's existing terminal rename barriers.
func (r *partitionConstraintReplay) virtualUniqueRenames(view catalog.Read, index *catalog.Relation) string {
	if partitionReplayOnlyExclusion(index) {
		return ""
	}
	type rename struct {
		index     *catalog.Relation
		generated string
	}
	var renames []rename
	var reserve func(*catalog.Relation)
	reserve = func(parentIndex *catalog.Relation) {
		table := view.MustRelation(parentIndex.SQLIndex.Parent)
		if table.Partition == nil {
			return
		}
		for _, address := range table.Partition.Children {
			childIndex := partitionDumpIndexBacking(view, parentIndex.Address(), address)
			child := view.MustRelation(address)
			used := r.occupied[child.Namespace()]
			if used == nil {
				used = make(map[string]bool)
				r.occupied[child.Namespace()] = used
			}
			suffix, addition := "key", ""
			if childIndex.SQLIndex.IsExclusion() {
				suffix = "excl"
			}
			if childIndex.SQLIndex.Constraint.Primary {
				suffix = "pkey"
			} else {
				labels := make([]string, len(childIndex.SQLIndex.Keys))
				for i := range childIndex.SQLIndex.Keys {
					labels[i] = childIndex.Columns[i].Name
				}
				addition = strings.Join(labels, "_")
			}
			name := generatedConstraintName(child.Name(), addition, suffix, used)
			used[name] = true
			renames = append(renames, rename{childIndex, name})
			reserve(childIndex)
		}
	}
	reserve(index)
	var sql strings.Builder
	for _, rename := range renames {
		target := r.temporary[rename.index.Address()]
		sql.WriteString("ALTER INDEX " + quoteIdentifier(view.Get(rename.index.Namespace()).Name()) + "." + quoteIdentifier(rename.generated) + " RENAME TO " + quoteIdentifier(target) + ";\n")
		used := r.occupied[rename.index.Namespace()]
		delete(used, rename.generated)
		used[target] = true
	}
	return sql.String()
}

func partitionVirtualUniqueMembershipReady(view catalog.Read, graph map[dumpEventKey][]dumpEventKey, event dumpEventKey, table *catalog.Relation) {
	member := dumpEventKey{oid: table.Address().ObjectID}
	if partitionDumpNeedsAttach(view, table) {
		member.phase = dumpTableAttachment
	}
	addDumpDependency(graph, event, member)
	if table.Partition != nil {
		for _, address := range table.Partition.Children {
			partitionVirtualUniqueMembershipReady(view, graph, event, view.MustRelation(address))
		}
	}
}

func partitionReplayTopologyReady(view catalog.Read, table *catalog.Relation) bool {
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if ok && index.SQLIndex != nil && index.SQLIndex.Parent == table.Address() && (partitionPreservedReplayBacking(view, index) || partitionReplayVirtualUniqueRoot(view, index) != nil) {
			return true
		}
	}
	return false
}
