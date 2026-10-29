package partition

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
)

// FreshReplay is one ordinary temporary-parent recipe in the existing scratch
// preparation/completion phases. Equivalent providers share one attachment.
// Invalid adopted subtrees require recursive seed ADD after table attachment.
type FreshReplay struct {
	Scratch   string
	Parent    *catalog.Relation
	Indexes   []*catalog.Relation
	Seeds     []string
	Recursive bool
	// A false-origin invalid root whose captured children are all valid needs an
	// invalid temporary child. Captured memberships follow completion instead.
	InvalidChild, InvalidGap string
	InvalidSeeds             []string
	Created                  []string
	TransientIndexes         []string
	Open, Close              bool
	Guards                   []*catalog.Relation
	GuardSeeds               []string
	CleanupSeeds             []string
}

func freshReplayGroups(view catalog.Read, table *catalog.Relation) ([]FreshReplay, error) {
	var groups []FreshReplay
	for _, idx := range index.PartitionIndexDeclarationOrder(view, table) {
		if origin := index.PartitionReplayFreshOrigin(view, idx); origin == nil || origin.Address() != idx.Address() {
			continue
		}
		placed := false
		for i := range groups {
			match, err := index.PartitionIndexEquivalent(context.Background(), view, table, groups[i].Indexes[0].SQLIndex, idx.SQLIndex)
			if err != nil {
				return nil, err
			}
			if match {
				groups[i].Indexes = append(groups[i].Indexes, idx)
				groups[i].Recursive = groups[i].Recursive || !idx.SQLIndex.Valid
				placed = true
				break
			}
		}
		if placed {
			continue
		}
		parent := *table
		parent.Membership = nil
		parent.Partition = nil
		for i, key := range idx.SQLIndex.Keys {
			if key.Derived != nil || !idx.SQLIndex.Exclusion[i].IsEquality() {
				continue
			}
			usage, err := index.KeyUsage(table, key)
			if err != nil {
				return nil, err
			}
			usage = index.KeyCollation(usage, key)
			class, ok := view.UniqueDefaultIndexClass(403, usage.Type)
			if !ok {
				continue
			}
			comparison, err := routine.BindIndexOrdering(view, usage, class)
			if err != nil {
				continue
			}
			entry := catalog.PartitionKeyEntry{Attribute: key.Attribute, Usage: usage, Class: class, Comparison: comparison, Label: table.Columns[table.IndexForAttribute(key.Attribute)].Name}
			if usage.Collation != table.Columns[table.IndexForAttribute(key.Attribute)].Usage.Collation {
				entry.Collation = usage.Collation.ID
			}
			parent.Partition = &catalog.PartitionDefinition{Strategy: parser.PartitionRange, Keys: []catalog.PartitionKeyEntry{entry}}
			if index.PartitionUniqueCoverage(view, &parent, idx.SQLIndex) == nil {
				break
			}
			parent.Partition = nil
		}
		if parent.Partition == nil {
			return nil, fmt.Errorf("stuntgres: exclusion replay lacks a compatible equality partition key")
		}
		groups = append(groups, FreshReplay{Parent: &parent, Indexes: []*catalog.Relation{idx}, Recursive: !idx.SQLIndex.Valid})
	}
	var stages []FreshReplay
	for i := range groups {
		group := &groups[i]
		group.Open, group.Close = true, true
		for j := range table.Constraints.Exclusion {
			candidate := view.MustRelation(table.Constraints.Exclusion[j].Index)
			if !index.PartitionReplayCompetingExclusion(view, candidate) {
				continue
			}
			match, err := index.PartitionIndexEquivalent(context.Background(), view, table, group.Indexes[0].SQLIndex, candidate.SQLIndex)
			if err != nil {
				return nil, err
			}
			if match {
				group.Guards = append(group.Guards, candidate)
				group.Recursive = group.Recursive || !candidate.SQLIndex.Valid
			}
		}
		var stale, retained []*catalog.Relation
		for _, idx := range group.Indexes {
			if !freshNeedsMembers(view, idx) {
				stale = append(stale, idx)
			} else {
				retained = append(retained, idx)
			}
		}
		if len(stale) != 0 && len(retained) != 0 {
			first, last := *group, *group
			first.Indexes, first.Close, first.Recursive = stale, false, true
			last.Indexes, last.Open, last.Recursive, last.Guards = retained, false, true, nil
			stages = append(stages, first, last)
		} else {
			stages = append(stages, *group)
		}
	}
	return stages, nil
}

// Descendants normally exist before attachment to the temporary parent. A
// preserved-invalid false origin instead uses the temporary invalid child and
// creates its captured valid children after completion, without healing it.
func freshReplayReady(r dump.Resolver, view catalog.Read, graph dump.Graph, prepare dump.Event, idx *catalog.Relation) {
	table := view.MustRelation(idx.SQLIndex.Parent)
	graph.Add(prepare, dump.Event{OID: table.Address().ObjectID})
	if table.Partition == nil || index.PartitionIndexPreservedInvalid(view, idx) || freshEarlySplit(view, idx) {
		return
	}
	for _, address := range table.Partition.Children {
		child := view.MustRelation(address)
		member := dump.Event{OID: address.ObjectID}
		if DumpNeedsAttach(view, child) {
			member.Phase = dump.TableAttachment
		}
		graph.Add(prepare, member)
		backing := index.PartitionDumpIndexBacking(view, idx.Address(), address)
		if backing == nil {
			continue
		}
		if c := index.ExclusionDefinition(child, backing); c != nil && c.NoInherit {
			graph.Add(prepare, constraintCreator(r, view, backing))
		}
		freshReplayReady(r, view, graph, prepare, backing)
	}
}

func freshOriginTable(view catalog.Read, table *catalog.Relation) bool {
	for _, c := range table.Constraints.Exclusion {
		if origin := index.PartitionReplayFreshOrigin(view, view.MustRelation(c.Index)); origin != nil && origin.Address() == c.Index {
			return true
		}
	}
	return false
}

func freshCompletion(view catalog.Read, table *catalog.Relation) dump.Event {
	var last *catalog.Relation
	for i := range table.Constraints.Exclusion {
		idx := view.MustRelation(table.Constraints.Exclusion[i].Index)
		if !freshReplayRoot(view, idx) || freshGroupRoot(view, idx) != idx {
			continue
		}
		last = freshStageRoot(view, idx, true)
		if last == nil {
			last = freshStageRoot(view, idx, false)
		}
	}
	return dump.Event{OID: last.Address().ObjectID, Phase: dump.InvalidIndexCompletion}
}

func freshReplayAncestor(view catalog.Read, table *catalog.Relation) *catalog.Relation {
	for {
		if freshOriginTable(view, table) {
			return table
		}
		if table.Membership == nil {
			return nil
		}
		table = view.MustRelation(table.Membership.Parent)
	}
}

func freshReplayPreservesInvalid(view catalog.Read, table *catalog.Relation) bool {
	for i := range table.Constraints.Exclusion {
		c := &table.Constraints.Exclusion[i]
		idx := view.MustRelation(c.Index)
		origin := index.PartitionReplayFreshOrigin(view, idx)
		if origin != nil && origin.Address() == idx.Address() && index.PartitionIndexPreservedInvalid(view, idx) {
			return true
		}
	}
	return false
}

func freshReplayRoot(view catalog.Read, idx *catalog.Relation) bool {
	origin := index.PartitionReplayFreshOrigin(view, idx)
	return origin != nil && origin.Address() == idx.Address()
}

func freshEquivalent(view catalog.Read, table, a, b *catalog.Relation) bool {
	match, err := index.PartitionIndexEquivalent(context.Background(), view, table, a.SQLIndex, b.SQLIndex)
	return err == nil && match
}

func freshGroupRoot(view catalog.Read, idx *catalog.Relation) *catalog.Relation {
	table := view.MustRelation(idx.SQLIndex.Parent)
	for i := range table.Constraints.Exclusion {
		other := view.MustRelation(table.Constraints.Exclusion[i].Index)
		if freshReplayRoot(view, other) && freshEquivalent(view, table, idx, other) {
			return other
		}
	}
	return nil
}

func freshStageRoot(view catalog.Read, idx *catalog.Relation, members bool) *catalog.Relation {
	table := view.MustRelation(idx.SQLIndex.Parent)
	for i := range table.Constraints.Exclusion {
		other := view.MustRelation(table.Constraints.Exclusion[i].Index)
		if freshReplayRoot(view, other) && freshNeedsMembers(view, other) == members && freshEquivalent(view, table, idx, other) {
			return other
		}
	}
	return nil
}

func freshCreator(view catalog.Read, idx *catalog.Relation) dump.Event {
	root := freshStageRoot(view, idx, freshNeedsMembers(view, idx))
	phase := dump.InvalidIndexPreparation
	if freshStageInvalid(view, idx) {
		phase = dump.InvalidIndexCompletion
	}
	return dump.Event{OID: root.Address().ObjectID, Phase: phase}
}

// A recursive fresh ADD must see this adopted origin after its table already
// belongs to the target. It is not a candidate for that earlier table ATTACH.
func freshAdoptedAfterMembership(view catalog.Read, idx *catalog.Relation) bool {
	if idx.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
		return false
	}
	parent := view.MustRelation(idx.SQLIndex.PartitionParent)
	if !freshReplayRoot(view, parent) {
		return false
	}
	if !parent.SQLIndex.Valid && !index.PartitionIndexPreservedInvalid(view, parent) {
		return true
	}
	table := view.MustRelation(idx.SQLIndex.Parent)
	current := index.ExclusionDefinition(table, idx)
	if current == nil {
		return false
	}
	for i := range table.Constraints.Exclusion {
		other := &table.Constraints.Exclusion[i]
		if other.NoInherit || other.Parent == 0 || other.Parent == current.Parent {
			continue
		}
		if freshEquivalent(view, table, idx, view.MustRelation(other.Index)) {
			return true
		}
	}
	return false
}

// ReplayWholeBoundSQL is the ordinary whole-space bound for a temporary child.
// HASH has no DEFAULT partition; use the existing hash bound renderer.
func ReplayWholeBoundSQL(parent *catalog.Relation) (string, error) {
	if parent.Partition == nil {
		return "", fmt.Errorf("partition replay requires a partitioned parent")
	}
	switch parent.Partition.Strategy {
	case parser.PartitionRange, parser.PartitionList:
		return " DEFAULT", nil
	case parser.PartitionHash:
		return hashBoundSQL(&catalog.PartitionMembership{Modulus: 1, Remainder: 0}), nil
	default:
		return "", fmt.Errorf("unsupported partition replay strategy")
	}
}

// An empty DEFAULT target can coexist with this sentinel-free non-NULL gap.
// The gap survives explicit index promotion and is removed before any rows.
func ReplayPromotionBoundSQL(parent *catalog.Relation) (string, error) {
	if parent.Partition == nil || parent.Partition.Strategy != parser.PartitionRange {
		return "", fmt.Errorf("stuntgres: promotion gap requires a RANGE partition key")
	}
	var text strings.Builder
	text.WriteString(" FOR VALUES FROM (")
	for i := range parent.Partition.Keys {
		if i != 0 {
			text.WriteString(", ")
		}
		text.WriteString("MINVALUE")
	}
	text.WriteString(") TO (")
	for i := range parent.Partition.Keys {
		if i != 0 {
			text.WriteString(", ")
		}
		text.WriteString("MAXVALUE")
	}
	text.WriteString(")")
	return text.String(), nil
}

func freshNeedsMembers(view catalog.Read, idx *catalog.Relation) bool {
	if !idx.SQLIndex.Valid && !index.PartitionIndexPreservedInvalid(view, idx) {
		return true
	}
	table := view.MustRelation(idx.SQLIndex.Parent)
	if table.Partition != nil {
		for _, address := range table.Partition.Children {
			child := index.PartitionDumpIndexBacking(view, idx.Address(), address)
			if child == nil {
				continue
			}
			if c := index.ExclusionDefinition(view.MustRelation(address), child); c != nil && c.NoInherit && freshAdoptedAfterMembership(view, child) {
				return true
			}
		}
	}
	return false
}

func freshStageInvalid(view catalog.Read, idx *catalog.Relation) bool {
	table := view.MustRelation(idx.SQLIndex.Parent)
	needs := freshNeedsMembers(view, idx)
	for i := range table.Constraints.Exclusion {
		other := view.MustRelation(table.Constraints.Exclusion[i].Index)
		if freshReplayRoot(view, other) && freshNeedsMembers(view, other) == needs && index.PartitionIndexPreservedInvalid(view, other) && freshEquivalent(view, table, idx, other) {
			return true
		}
	}
	return false
}

func freshEarlySplit(view catalog.Read, idx *catalog.Relation) bool {
	if freshNeedsMembers(view, idx) {
		return false
	}
	table := view.MustRelation(idx.SQLIndex.Parent)
	for i := range table.Constraints.Exclusion {
		other := view.MustRelation(table.Constraints.Exclusion[i].Index)
		if freshReplayRoot(view, other) && freshNeedsMembers(view, other) && freshEquivalent(view, table, idx, other) {
			return true
		}
	}
	return false
}

func freshReplayDelaysMembership(view catalog.Read, table *catalog.Relation) bool {
	if freshReplayPreservesInvalid(view, table) {
		return true
	}
	for i := range table.Constraints.Exclusion {
		idx := view.MustRelation(table.Constraints.Exclusion[i].Index)
		if freshReplayRoot(view, idx) && freshEarlySplit(view, idx) {
			return true
		}
	}
	return false
}
