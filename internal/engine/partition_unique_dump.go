package engine

import (
	"fmt"
	"strings"
)

// A delayed virtual local definition creates its entire own constraint/index
// tree. Descendant identities therefore use that definition's graph provider.
func partitionReplayVirtualUniqueRoot(view CatalogRead, index *relationDescriptor) *relationDescriptor {
	if index.sqlIndex == nil || index.sqlIndex.constraint == nil || index.sqlIndex.constraint.kind != uniqueBackingKind {
		return nil
	}
	for index.sqlIndex.partitionParent != (ObjectAddress{}) {
		index = view.state.descriptors[index.sqlIndex.partitionParent].(*relationDescriptor)
		if index.sqlIndex.constraint == nil || index.sqlIndex.constraint.kind != uniqueBackingKind {
			return nil
		}
	}
	table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	if table.partition != nil && partitionPreservedReplayLocal(view, index) {
		return index
	}
	return nil
}

func partitionReplaySeparateConstraint(view CatalogRead, index *relationDescriptor) bool {
	return partitionReplayUsingIndex(view, index) || partitionReplayVirtualUniqueRoot(view, index) != nil
}

func partitionVirtualUniqueDefinitionSQL(view CatalogRead, index *relationDescriptor) (string, error) {
	table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
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
			if childIndex.sqlIndex.constraint.Primary {
				suffix = "pkey"
			} else {
				labels := make([]string, len(childIndex.sqlIndex.keys))
				for i, key := range childIndex.sqlIndex.keys {
					labels[i] = child.columns[child.indexForAttribute(key.attribute)].Name
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
