package partition

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
)

func constraintCreator(r dump.Resolver, view catalog.Read, idx *catalog.Relation) dump.Event {
	if origin := index.PartitionReplayFreshOrigin(view, idx); origin != nil {
		if origin.Address() == idx.Address() || !freshStageInvalid(view, origin) && !freshEarlySplit(view, origin) {
			return freshCreator(view, origin)
		}
		// Captured valid descendants follow the stale-root completion.
	}
	root := index.PartitionReplayVirtualUniqueRoot(view, idx)
	if root != nil && (!index.PartitionReplayOnlyExclusion(view, root) || root.Address() == idx.Address()) {
		return dump.Event{OID: root.Address().ObjectID}
	}
	// Independently declared providers exist before membership adopts them.
	table := view.MustRelation(idx.SQLIndex.Parent)
	local := index.ExclusionDefinition(table, idx)
	if !idx.SQLIndex.IsExclusion() || idx.SQLIndex.PartitionParent == (ids.ObjectAddress{}) || local != nil && local.NoInherit || index.PartitionReplayUsingIndex(view, idx) {
		oid, _ := r.Predecessor(view, idx.Address(), ids.ObjectAddress{})
		return dump.Event{OID: oid}
	}
	table = view.MustRelation(idx.SQLIndex.Parent)
	creator := dump.Event{OID: table.Address().ObjectID}
	for idx.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		table = view.MustRelation(idx.SQLIndex.Parent)
		if c := index.ExclusionDefinition(table, idx); c != nil && c.NoInherit {
			break
		}
		if origin := index.PartitionReplayFreshOrigin(view, idx); origin != nil && origin.Address() == idx.Address() {
			break
		}
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
		if index.PartitionReplayOnlyExclusion(view, idx) && index.PartitionDumpIndexBacking(view, idx.Address(), gap.Address()) == nil && index.PartitionDumpIndexBacking(view, idx.Address(), sibling.Address()) != nil {
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
		if index.PartitionReplayOnlyExclusion(view, idx) {
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
	if index.PartitionReplayOnlyExclusion(view, idx) {
		return ""
	}
	table := view.MustRelation(idx.SQLIndex.Parent)
	var roots []*catalog.Relation
	for _, address := range table.Partition.Children {
		if child := index.PartitionDumpIndexBacking(view, idx.Address(), address); child != nil {
			roots = append(roots, child)
		}
	}
	return r.CreationRenames(view, roots, dump.Event{OID: idx.Address().ObjectID})
}

func virtualUniqueMembershipReady(r dump.Resolver, view catalog.Read, graph dump.Graph, event dump.Event, table, idx *catalog.Relation) {
	member := dump.Event{OID: table.Address().ObjectID}
	if DumpNeedsAttach(view, table) {
		member.Phase = dump.TableAttachment
	}
	graph.Add(event, member)
	if table.Partition != nil {
		for _, address := range table.Partition.Children {
			childTable := view.MustRelation(address)
			var child *catalog.Relation
			if idx != nil {
				child = index.PartitionDumpIndexBacking(view, idx.Address(), address)
			}
			if child != nil {
				if c := index.ExclusionDefinition(childTable, child); c != nil && c.NoInherit {
					graph.Add(event, constraintCreator(r, view, child))
				}
			}
			virtualUniqueMembershipReady(r, view, graph, event, childTable, child)
		}
	}
}

// A separate provider creator needs topology, not a completed data subtree,
// before crossing table membership. Fresh-origin scratch creation has the same
// requirement as delayed virtual declarations; waiting for completion would
// make a later ONLY ancestor declaration depend on its own terminal-name rows.
func replayTopologyReady(view catalog.Read, table *catalog.Relation) bool {
	for _, descriptor := range view.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if ok && idx.SQLIndex != nil && idx.SQLIndex.Parent == table.Address() && (preservedReplayBacking(view, idx) || index.PartitionReplayVirtualUniqueRoot(view, idx) != nil || index.PartitionReplayFreshOrigin(view, idx) != nil) {
			return true
		}
	}
	return false
}
