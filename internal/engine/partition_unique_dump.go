package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/ids"
)

// Delayed virtual definitions own a local constraint origin. Recursive UNIQUE
// creates its tree at declaration; incomplete EXCLUDE uses ONLY and memberships.
func partitionReplayVirtualUniqueRoot(view CatalogRead, index *relationDescriptor) *relationDescriptor {
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

func partitionReplaySeparateConstraint(view CatalogRead, index *relationDescriptor) bool {
	return partitionReplayUsingIndex(view, index) || partitionReplayVirtualUniqueRoot(view, index) != nil
}

// ONLY declarations create just the local origin. Captured covered providers
// are created later by their table membership, never by index attachment.
func partitionReplayOnlyExclusion(index *relationDescriptor) bool {
	return index.SQLIndex != nil && index.SQLIndex.isExclusion() && !index.SQLIndex.Valid && index.SQLIndex.PartitionParent == (ids.ObjectAddress{})
}

func partitionConstraintCreator(view CatalogRead, index *relationDescriptor) dumpEventKey {
	root := partitionReplayVirtualUniqueRoot(view, index)
	if root != nil && (!partitionReplayOnlyExclusion(root) || root.address == index.address) {
		return dumpEventKey{oid: root.address.ObjectID}
	}
	// UNIQUE can adopt a provider declared on the standalone child before
	// ATTACH. Its name is an attachment prerequisite, not an attachment output.
	// Only EXCLUDE's mandatory fresh clones use membership creator readiness.
	if !index.SQLIndex.isExclusion() || index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) || partitionReplayUsingIndex(view, index) {
		oid, _ := dumpPredecessor(view, index.address, ids.ObjectAddress{})
		return dumpEventKey{oid: oid}
	}
	table := view.MustRelation(index.SQLIndex.Parent)
	creator := dumpEventKey{oid: table.address.ObjectID}
	for index.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		table = view.MustRelation(index.SQLIndex.Parent)
		if partitionDumpNeedsAttach(view, table) {
			creator = dumpEventKey{oid: table.address.ObjectID, phase: dumpTableAttachment}
		}
		index = view.MustRelation(index.SQLIndex.PartitionParent)
	}
	return creator
}

// Remove only DEFAULT-last edges contradicted by an ONLY origin's captured
// gap-before-declaration-before-covered-membership requirements.
func partitionExclusionDefaultBefore(view CatalogRead, gap, sibling *relationDescriptor) bool {
	if gap.Membership == nil || !gap.Membership.DefaultBound {
		return false
	}
	parent := view.MustRelation(gap.Membership.Parent)
	for _, index := range partitionIndexDeclarationOrder(view, parent) {
		if partitionReplayOnlyExclusion(index) && partitionDumpIndexBacking(view, index.address, gap.address) == nil && partitionDumpIndexBacking(view, index.address, sibling.address) != nil {
			return true
		}
	}
	return false
}

func partitionVirtualUniqueDefinitionSQL(view CatalogRead, index *relationDescriptor) (string, error) {
	table := view.MustRelation(index.SQLIndex.Parent)
	if index.SQLIndex.isExclusion() {
		text, err := indexDefinitionSQL(context.Background(), view, index, indexPresentation{dump: true, exclusion: true})
		if err != nil {
			return "", err
		}
		only := ""
		if partitionReplayOnlyExclusion(index) {
			only = "ONLY "
		}
		return "ALTER TABLE " + only + quoteIdentifier(view.Get(table.namespace).Name()) + "." + quoteIdentifier(table.name) + " ADD CONSTRAINT " + quoteIdentifier(index.name) + " " + text + ";\n", nil
	}
	for _, constraint := range table.UniqueConstraints {
		if constraint.Index != index.address {
			continue
		}
		text, err := (pgRenderer{view: view}).uniqueConstraintSQL(table, &constraint)
		if err != nil {
			return "", err
		}
		return "ALTER TABLE " + quoteIdentifier(view.Get(table.namespace).Name()) + "." + quoteIdentifier(table.name) + " ADD CONSTRAINT " + quoteIdentifier(constraint.Name) + " " + text + ";\n", nil
	}
	return "", fmt.Errorf("stuntgres: missing virtual unique replay definition")
}

// Reserve the real recursive creation sequence before any RENAME frees a stem,
// exactly like existing inline partition constraint replay. Final names remain
// owned by the scheduler's existing terminal rename barriers.
func (r *partitionConstraintReplay) virtualUniqueRenames(view CatalogRead, index *relationDescriptor) string {
	if partitionReplayOnlyExclusion(index) {
		return ""
	}
	type rename struct {
		index     *relationDescriptor
		generated string
	}
	var renames []rename
	var reserve func(*relationDescriptor)
	reserve = func(parentIndex *relationDescriptor) {
		table := view.MustRelation(parentIndex.SQLIndex.Parent)
		if table.Partition == nil {
			return
		}
		for _, address := range table.Partition.Children {
			childIndex := partitionDumpIndexBacking(view, parentIndex.address, address)
			child := view.MustRelation(address)
			used := r.occupied[child.namespace]
			if used == nil {
				used = make(map[string]bool)
				r.occupied[child.namespace] = used
			}
			suffix, addition := "key", ""
			if childIndex.SQLIndex.isExclusion() {
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
			name := generatedConstraintName(child.name, addition, suffix, used)
			used[name] = true
			renames = append(renames, rename{childIndex, name})
			reserve(childIndex)
		}
	}
	reserve(index)
	var sql strings.Builder
	for _, rename := range renames {
		target := r.temporary[rename.index.address]
		sql.WriteString("ALTER INDEX " + quoteIdentifier(view.Get(rename.index.namespace).Name()) + "." + quoteIdentifier(rename.generated) + " RENAME TO " + quoteIdentifier(target) + ";\n")
		used := r.occupied[rename.index.namespace]
		delete(used, rename.generated)
		used[target] = true
	}
	return sql.String()
}

func partitionVirtualUniqueMembershipReady(view CatalogRead, graph map[dumpEventKey][]dumpEventKey, event dumpEventKey, table *relationDescriptor) {
	member := dumpEventKey{oid: table.address.ObjectID}
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

func partitionReplayTopologyReady(view CatalogRead, table *relationDescriptor) bool {
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*relationDescriptor)
		if ok && index.SQLIndex != nil && index.SQLIndex.Parent == table.address && (partitionPreservedReplayBacking(view, index) || partitionReplayVirtualUniqueRoot(view, index) != nil) {
			return true
		}
	}
	return false
}
