package partition

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
)

func constraintCreator(r dump.Resolver, view catalog.Read, idx *catalog.Relation) dump.Event {
	root := index.PartitionReplayVirtualUniqueRoot(view, idx)
	if root != nil && (!index.PartitionReplayOnlyExclusion(root) || root.Address() == idx.Address()) {
		return dump.Event{OID: root.Address().ObjectID}
	}
	// UNIQUE can adopt a provider declared on the standalone child before
	// ATTACH. Its name is an attachment prerequisite, not an attachment output.
	// Only EXCLUDE's mandatory fresh clones use membership creator readiness.
	if !idx.SQLIndex.IsExclusion() || idx.SQLIndex.PartitionParent == (ids.ObjectAddress{}) || index.PartitionReplayUsingIndex(view, idx) {
		oid, _ := r.Predecessor(view, idx.Address(), ids.ObjectAddress{})
		return dump.Event{OID: oid}
	}
	table := view.MustRelation(idx.SQLIndex.Parent)
	creator := dump.Event{OID: table.Address().ObjectID}
	for idx.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		table = view.MustRelation(idx.SQLIndex.Parent)
		if DumpNeedsAttach(view, table) {
			creator = dump.Event{OID: table.Address().ObjectID, Phase: dump.TableAttachment}
		}
		idx = view.MustRelation(idx.SQLIndex.PartitionParent)
	}
	return creator
}

// Remove only DEFAULT-last edges contradicted by an ONLY origin's captured
// gap-before-declaration-before-covered-membership requirements.
func exclusionDefaultBefore(view catalog.Read, gap, sibling *catalog.Relation) bool {
	if gap.Membership == nil || !gap.Membership.DefaultBound {
		return false
	}
	parent := view.MustRelation(gap.Membership.Parent)
	for _, idx := range index.PartitionIndexDeclarationOrder(view, parent) {
		if index.PartitionReplayOnlyExclusion(idx) && index.PartitionDumpIndexBacking(view, idx.Address(), gap.Address()) == nil && index.PartitionDumpIndexBacking(view, idx.Address(), sibling.Address()) != nil {
			return true
		}
	}
	return false
}

func VirtualUniqueDefinitionSQL(view catalog.Read, idx *catalog.Relation) (string, error) {
	table := view.MustRelation(idx.SQLIndex.Parent)
	if idx.SQLIndex.IsExclusion() {
		text, err := index.DefinitionSQL(context.Background(), view, idx, index.Presentation{Dump: true, Exclusion: true})
		if err != nil {
			return "", err
		}
		only := ""
		if index.PartitionReplayOnlyExclusion(idx) {
			only = "ONLY "
		}
		return "ALTER TABLE " + only + catalog.QuoteIdentifier(view.Get(table.Namespace()).Name()) + "." + catalog.QuoteIdentifier(table.Name()) + " ADD CONSTRAINT " + catalog.QuoteIdentifier(idx.Name()) + " " + text + ";\n", nil
	}
	for _, constraint := range table.Constraints.Unique {
		if constraint.Index != idx.Address() {
			continue
		}
		text, err := (stored.Deparser{View: view}).UniqueConstraintSQL(table, &constraint)
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
func (r *ConstraintReplay) VirtualUniqueRenames(view catalog.Read, idx *catalog.Relation) string {
	if index.PartitionReplayOnlyExclusion(idx) {
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
			childIndex := index.PartitionDumpIndexBacking(view, parentIndex.Address(), address)
			child := view.MustRelation(address)
			used := r.Occupied[child.Namespace()]
			if used == nil {
				used = make(map[string]bool)
				r.Occupied[child.Namespace()] = used
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
	reserve(idx)
	var sql strings.Builder
	for _, rename := range renames {
		target := r.Temporary[rename.index.Address()]
		sql.WriteString("ALTER INDEX " + catalog.QuoteIdentifier(view.Get(rename.index.Namespace()).Name()) + "." + catalog.QuoteIdentifier(rename.generated) + " RENAME TO " + catalog.QuoteIdentifier(target) + ";\n")
		used := r.Occupied[rename.index.Namespace()]
		delete(used, rename.generated)
		used[target] = true
	}
	return sql.String()
}

func virtualUniqueMembershipReady(view catalog.Read, graph dump.Graph, event dump.Event, table *catalog.Relation) {
	member := dump.Event{OID: table.Address().ObjectID}
	if DumpNeedsAttach(view, table) {
		member.Phase = dump.TableAttachment
	}
	graph.Add(event, member)
	if table.Partition != nil {
		for _, address := range table.Partition.Children {
			virtualUniqueMembershipReady(view, graph, event, view.MustRelation(address))
		}
	}
}

func replayTopologyReady(view catalog.Read, table *catalog.Relation) bool {
	for _, descriptor := range view.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if ok && idx.SQLIndex != nil && idx.SQLIndex.Parent == table.Address() && (preservedReplayBacking(view, idx) || index.PartitionReplayVirtualUniqueRoot(view, idx) != nil) {
			return true
		}
	}
	return false
}
