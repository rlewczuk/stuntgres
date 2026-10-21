package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

func partitionConstraintCreator(view catalog.Read, index *catalog.Relation) dump.Event {
	root := partitionReplayVirtualUniqueRoot(view, index)
	if root != nil && (!partitionReplayOnlyExclusion(root) || root.Address() == index.Address()) {
		return dump.Event{OID: root.Address().ObjectID}
	}
	// UNIQUE can adopt a provider declared on the standalone child before
	// ATTACH. Its name is an attachment prerequisite, not an attachment output.
	// Only EXCLUDE's mandatory fresh clones use membership creator readiness.
	if !index.SQLIndex.IsExclusion() || index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) || partitionReplayUsingIndex(view, index) {
		oid, _ := dumpPredecessor(view, index.Address(), ids.ObjectAddress{})
		return dump.Event{OID: oid}
	}
	table := view.MustRelation(index.SQLIndex.Parent)
	creator := dump.Event{OID: table.Address().ObjectID}
	for index.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		table = view.MustRelation(index.SQLIndex.Parent)
		if partitionDumpNeedsAttach(view, table) {
			creator = dump.Event{OID: table.Address().ObjectID, Phase: dump.TableAttachment}
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
		return "ALTER TABLE " + only + catalog.QuoteIdentifier(view.Get(table.Namespace()).Name()) + "." + catalog.QuoteIdentifier(table.Name()) + " ADD CONSTRAINT " + catalog.QuoteIdentifier(index.Name()) + " " + text + ";\n", nil
	}
	for _, constraint := range table.Constraints.Unique {
		if constraint.Index != index.Address() {
			continue
		}
		text, err := (pgRenderer{view: view}).uniqueConstraintSQL(table, &constraint)
		if err != nil {
			return "", err
		}
		return "ALTER TABLE " + catalog.QuoteIdentifier(view.Get(table.Namespace()).Name()) + "." + catalog.QuoteIdentifier(table.Name()) + " ADD CONSTRAINT " + catalog.QuoteIdentifier(constraint.Name) + " " + text + ";\n", nil
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
			name := catalog.GeneratedConstraintName(child.Name(), addition, suffix, used)
			used[name] = true
			renames = append(renames, rename{childIndex, name})
			reserve(childIndex)
		}
	}
	reserve(index)
	var sql strings.Builder
	for _, rename := range renames {
		target := r.temporary[rename.index.Address()]
		sql.WriteString("ALTER INDEX " + catalog.QuoteIdentifier(view.Get(rename.index.Namespace()).Name()) + "." + catalog.QuoteIdentifier(rename.generated) + " RENAME TO " + catalog.QuoteIdentifier(target) + ";\n")
		used := r.occupied[rename.index.Namespace()]
		delete(used, rename.generated)
		used[target] = true
	}
	return sql.String()
}

func partitionVirtualUniqueMembershipReady(view catalog.Read, graph dump.Graph, event dump.Event, table *catalog.Relation) {
	member := dump.Event{OID: table.Address().ObjectID}
	if partitionDumpNeedsAttach(view, table) {
		member.Phase = dump.TableAttachment
	}
	graph.Add(event, member)
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
