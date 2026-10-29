package partitioncmds

import (
	"reflect"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func replaySeedAdd(raw *parser.AlterTable) bool {
	return raw != nil && len(raw.Name.Parts) == 2 && len(raw.Actions) == 1 && raw.Actions[0].Action == parser.AddExclusion && raw.Actions[0].Exclusion != nil && strings.HasPrefix(raw.Name.Parts[1].Text, partition.ReplayStem) && strings.HasPrefix(raw.Actions[0].Exclusion.Name.Text, partition.ReplayStem)
}

// PreparePartitionReplayExtension fences accepted authority before the ordinary
// ADD changes table revisions. Only its exact successful statement can extend it.
func PreparePartitionReplayExtension(e *core.Engine, op *session.Operation, raw *parser.AlterTable, records map[ids.ObjectAddress]partition.ReplayTeardown) error {
	if !replaySeedAdd(raw) || len(records) == 0 {
		return nil
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	ref, err := e.ResolveRelationReference(auth, raw.Name, true)
	if err != nil {
		return err
	}
	var candidates []partition.ReplayTeardown
	for _, record := range records {
		if record.Kind != partition.ReplayInvalidChild {
			continue
		}
		peer, ok := auth.Catalog.Relation(record.Peer.Address)
		if !ok || raw.Only && ref != record.Target || !raw.Only && (peer.Membership == nil || peer.Membership.Parent != ref.Address) {
			continue
		}
		if e.ImportTargets == nil || record.Extension != nil || !acceptedInvalidCaptureLocked(e, auth.Catalog, &record, ref) {
			return replayTeardownError()
		}
		record.Extension = &partition.ReplayTeardownExtension{Statement: raw, Before: auth.Catalog, Relation: ref}
		candidates = append(candidates, record)
	}
	for _, record := range candidates {
		records[record.Target.Address] = record
	}
	return nil
}

func acceptedInvalidCaptureLocked(e *core.Engine, view catalog.Read, record *partition.ReplayTeardown, seedParent ids.ObjectRef) bool {
	target, ok := view.Relation(record.Target.Address)
	peer, live := view.Relation(record.Peer.Address)
	gap, present := view.Relation(record.Gap.Address)
	if !ok || !live || !present || e.Catalog.Domain.Ref(view.Epoch(), target.Address()) != record.Target || e.Catalog.Domain.Ref(view.Epoch(), peer.Address()) != record.Peer || e.Catalog.Domain.Ref(view.Epoch(), gap.Address()) != record.Gap || target.Owner != record.Owner || peer.Owner != record.PeerOwner || gap.Owner != record.GapOwner || target.Revision() != record.TargetRevision || gap.Revision() != record.GapRevision || !replayIndexesMatch(e, view, record) {
		return false
	}
	if peer.Revision() != record.PeerRevision {
		// The first seed follows the ordinary attachment of its still-unconstrained
		// target. Permit only that exact membership delta on the captured peer.
		old := record.PeerDefinition
		if record.Complete || old == nil || old.Membership != nil || peer.Membership == nil {
			return false
		}
		parent, exists := view.Relation(peer.Membership.Parent)
		if !exists || e.Catalog.Domain.Ref(view.Epoch(), parent.Address()) != seedParent || !strings.HasPrefix(parent.Name(), partition.ReplayStem) || !wholeReplayMember(parent, peer) || len(parent.Partition.Children) != 1 || parent.Partition.Children[0] != peer.Address() {
			return false
		}
		copy := *peer
		copy.ObjectRevision, copy.Membership = old.ObjectRevision, old.Membership
		if !reflect.DeepEqual(copy, *old) {
			return false
		}
	}
	if record.Complete {
		return validateInvalidChildLocked(e, view, target, peer, record)
	}
	if target.Partition == nil || len(target.Partition.Children) != 1 || target.Partition.Children[0] != gap.Address() || peer.Partition == nil || len(peer.Partition.Children) != 1 || peer.Partition.Children[0] != target.Address() || !wholeReplayMember(peer, target) || !wholeReplayMember(target, gap) || !emptyReplayLeaf(e, gap) {
		return false
	}
	for _, b := range record.Backings {
		c := replayExclusion(target, b.Constraint)
		if c == nil || !c.NoInherit || c.Parent != b.Parent || c.Index != b.Index.Address || c.IsLocal != (b.Parent == 0) {
			return false
		}
		idx := replayExclusionIndex(view, target, c)
		if idx == nil || idx.SQLIndex.Valid || idx.Revision() != b.Revision || e.Catalog.Domain.Ref(view.Epoch(), idx.Address()) != b.Index || idx.SQLIndex.PartitionParent != b.ParentIndex.Address {
			return false
		}
		if b.Parent != 0 {
			parent, exists := view.Relation(b.ParentIndex.Address)
			if !exists || parent.Revision() != b.ParentRevision || e.Catalog.Domain.Ref(view.Epoch(), parent.Address()) != b.ParentIndex {
				return false
			}
		}
	}
	return len(record.Backings) != 0
}

func replaySeedAncestor(view catalog.Read, idx *catalog.Relation, seed ids.ObjectAddress) bool {
	for idx != nil && idx.SQLIndex != nil {
		if idx.Address() == seed {
			return true
		}
		if idx.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
			return false
		}
		var ok bool
		idx, ok = view.Relation(idx.SQLIndex.PartitionParent)
		if !ok {
			return false
		}
	}
	return false
}

// The admitted ADD may append its own inherited providers and adopt a captured
// local root. No other old definition or backing revision may be renewed.
func replayExtensionMatches(e *core.Engine, view catalog.Read, record *partition.ReplayTeardown, raw *parser.AlterTable, ref ids.ObjectRef) bool {
	x := record.Extension
	if x == nil || x.Statement != raw || x.Relation != ref {
		return false
	}
	seedTable, ok := view.Relation(ref.Address)
	if !ok || e.Catalog.Domain.Ref(view.Epoch(), ref.Address) != ref {
		return false
	}
	var seed *catalog.Relation
	for i := range seedTable.Constraints.Exclusion {
		c := &seedTable.Constraints.Exclusion[i]
		if c.Name == raw.Actions[0].Exclusion.Name.Text {
			seed = replayExclusionIndex(view, seedTable, c)
		}
	}
	if seed == nil {
		return false
	}
	if _, existed := x.Before.Relation(seed.Address()); existed {
		return false
	}
	addresses := [4]ids.ObjectAddress{record.Peer.Address, record.Target.Address, record.Gap.Address, ref.Address}
	scope := addresses[:]
	if raw.Only {
		scope = addresses[:3]
	}
	for _, address := range scope {
		old, existed := x.Before.Relation(address)
		next, exists := view.Relation(address)
		if !existed || !exists {
			return false
		}
		copy := *next
		copy.ObjectRevision = old.ObjectRevision
		copy.Constraints.Exclusion = old.Constraints.Exclusion
		if len(next.Constraints.Exclusion) < len(old.Constraints.Exclusion) {
			return false
		}
		for _, prior := range old.Constraints.Exclusion {
			current := replayExclusion(next, prior.OID)
			if current == nil {
				return false
			}
			adopted := !raw.Only && prior.Parent == 0 && prior.NoInherit && prior.IsLocal && current.Parent != 0 && !current.IsLocal
			constraint := *current
			if adopted {
				constraint.Parent, constraint.IsLocal = prior.Parent, prior.IsLocal
			}
			if !reflect.DeepEqual(constraint, prior) {
				return false
			}
			beforeIndex := x.Before.MustRelation(prior.Index)
			afterIndex := replayExclusionIndex(view, next, current)
			if afterIndex == nil {
				return false
			}
			if !adopted {
				if beforeIndex != afterIndex {
					return false
				}
				continue
			}
			if !replaySeedAncestor(view, afterIndex, seed.Address()) {
				return false
			}
			indexCopy, definition := *afterIndex, *afterIndex.SQLIndex
			indexCopy.ObjectRevision = beforeIndex.ObjectRevision
			definition.PartitionParent, definition.Constraint = beforeIndex.SQLIndex.PartitionParent, beforeIndex.SQLIndex.Constraint
			indexCopy.SQLIndex = &definition
			if !reflect.DeepEqual(indexCopy, *beforeIndex) {
				return false
			}
		}
		expected := *old
		for i := range next.Constraints.Exclusion {
			c := &next.Constraints.Exclusion[i]
			if replayExclusion(old, c.OID) != nil {
				continue
			}
			idx := replayExclusionIndex(view, next, c)
			if idx == nil || !replaySeedAncestor(view, idx, seed.Address()) || raw.Only && (address != ref.Address || c.Name != raw.Actions[0].Exclusion.Name.Text) {
				return false
			}
			if _, existed := x.Before.Relation(idx.Address()); existed {
				return false
			}
			// MarkIndexHistoryLocked publishes this sticky table fact with
			// its first actual backing. No other history field is relaxed.
			expected.History.HasIndexes = true
		}
		if !reflect.DeepEqual(copy, expected) {
			return false
		}
	}
	for _, descriptor := range view.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Parent != record.Target.Address && idx.SQLIndex.Parent != record.Gap.Address {
			continue
		}
		old, existed := x.Before.Relation(idx.Address())
		if existed {
			if old.Revision() == idx.Revision() {
				continue
			}
			if old.SQLIndex.PartitionParent != (ids.ObjectAddress{}) || !replaySeedAncestor(view, idx, seed.Address()) {
				return false
			}
		} else if !replaySeedAncestor(view, idx, seed.Address()) {
			return false
		}
	}
	for _, saved := range record.Indexes {
		if _, ok := view.Relation(saved.Ref.Address); !ok {
			return false
		}
	}
	return true
}
