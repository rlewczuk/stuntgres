package engine

import (
	"context"
	"fmt"
	"strings"
)

// Delayed virtual definitions own a local constraint origin. Recursive UNIQUE
// creates its tree at declaration; incomplete EXCLUDE uses ONLY and memberships.
func partitionReplayVirtualUniqueRoot(view CatalogRead, index *relationDescriptor) *relationDescriptor {
	if index.sqlIndex == nil || index.sqlIndex.constraint == nil {
		return nil
	}
	for index.sqlIndex.partitionParent != (ObjectAddress{}) {
		index = view.state.descriptors[index.sqlIndex.partitionParent].(*relationDescriptor)
		if index.sqlIndex.constraint == nil {
			return nil
		}
	}
	table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	if table.partition != nil && (partitionReplayOnlyExclusion(index) || partitionPreservedReplayLocal(view, index)) {
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
	return index.sqlIndex != nil && index.sqlIndex.isExclusion() && !index.sqlIndex.valid && index.sqlIndex.partitionParent == (ObjectAddress{})
}

func partitionConstraintCreator(view CatalogRead, index *relationDescriptor) dumpEventKey {
	root := partitionReplayVirtualUniqueRoot(view, index)
	if root != nil && (!partitionReplayOnlyExclusion(root) || root.address == index.address) {
		return dumpEventKey{oid: root.address.ObjectID}
	}
	// UNIQUE can adopt a provider declared on the standalone child before
	// ATTACH. Its name is an attachment prerequisite, not an attachment output.
	// Only EXCLUDE's mandatory fresh clones use membership creator readiness.
	if !index.sqlIndex.isExclusion() || index.sqlIndex.partitionParent == (ObjectAddress{}) || partitionReplayUsingIndex(view, index) {
		oid, _ := dumpPredecessor(view, index.address, ObjectAddress{})
		return dumpEventKey{oid: oid}
	}
	table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	creator := dumpEventKey{oid: table.address.ObjectID}
	for index.sqlIndex.partitionParent != (ObjectAddress{}) {
		table = view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		if partitionDumpNeedsAttach(view, table) {
			creator = dumpEventKey{oid: table.address.ObjectID, phase: dumpTableAttachment}
		}
		index = view.state.descriptors[index.sqlIndex.partitionParent].(*relationDescriptor)
	}
	return creator
}

// Remove only DEFAULT-last edges contradicted by an ONLY origin's captured
// gap-before-declaration-before-covered-membership requirements.
func partitionExclusionDefaultBefore(view CatalogRead, gap, sibling *relationDescriptor) bool {
	if gap.membership == nil || !gap.membership.defaultBound {
		return false
	}
	parent := view.state.descriptors[gap.membership.parent].(*relationDescriptor)
	for _, index := range partitionIndexDeclarationOrder(view, parent) {
		if partitionReplayOnlyExclusion(index) && partitionDumpIndexBacking(view, index.address, gap.address) == nil && partitionDumpIndexBacking(view, index.address, sibling.address) != nil {
			return true
		}
	}
	return false
}

func partitionVirtualUniqueDefinitionSQL(view CatalogRead, index *relationDescriptor) (string, error) {
	table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	if index.sqlIndex.isExclusion() {
		text, err := indexDefinitionSQL(context.Background(), view, index, indexPresentation{dump: true, exclusion: true})
		if err != nil {
			return "", err
		}
		only := ""
		if partitionReplayOnlyExclusion(index) {
			only = "ONLY "
		}
		return "ALTER TABLE " + only + quoteIdentifier(view.state.descriptors[table.namespace].Name()) + "." + quoteIdentifier(table.name) + " ADD CONSTRAINT " + quoteIdentifier(index.name) + " " + text + ";\n", nil
	}
	for _, constraint := range table.uniqueConstraints {
		if constraint.Index != index.address {
			continue
		}
		text, err := (pgRenderer{view: view}).uniqueConstraintSQL(table, &constraint)
		if err != nil {
			return "", err
		}
		return "ALTER TABLE " + quoteIdentifier(view.state.descriptors[table.namespace].Name()) + "." + quoteIdentifier(table.name) + " ADD CONSTRAINT " + quoteIdentifier(constraint.Name) + " " + text + ";\n", nil
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
		table := view.state.descriptors[parentIndex.sqlIndex.parent].(*relationDescriptor)
		if table.partition == nil {
			return
		}
		for _, address := range table.partition.children {
			childIndex := partitionDumpIndexBacking(view, parentIndex.address, address)
			child := view.state.descriptors[address].(*relationDescriptor)
			used := r.occupied[child.namespace]
			if used == nil {
				used = make(map[string]bool)
				r.occupied[child.namespace] = used
			}
			suffix, addition := "key", ""
			if childIndex.sqlIndex.isExclusion() {
				suffix = "excl"
			}
			if childIndex.sqlIndex.constraint.Primary {
				suffix = "pkey"
			} else {
				labels := make([]string, len(childIndex.sqlIndex.keys))
				for i := range childIndex.sqlIndex.keys {
					labels[i] = childIndex.columns[i].Name
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
		sql.WriteString("ALTER INDEX " + quoteIdentifier(view.state.descriptors[rename.index.namespace].Name()) + "." + quoteIdentifier(rename.generated) + " RENAME TO " + quoteIdentifier(target) + ";\n")
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
	if table.partition != nil {
		for _, address := range table.partition.children {
			partitionVirtualUniqueMembershipReady(view, graph, event, view.state.descriptors[address].(*relationDescriptor))
		}
	}
}

func partitionReplayTopologyReady(view CatalogRead, table *relationDescriptor) bool {
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if ok && index.sqlIndex != nil && index.sqlIndex.parent == table.address && (partitionPreservedReplayBacking(view, index) || partitionReplayVirtualUniqueRoot(view, index) != nil) {
			return true
		}
	}
	return false
}
