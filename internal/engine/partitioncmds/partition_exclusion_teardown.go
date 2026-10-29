package partitioncmds

import (
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func replayTeardownError() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore accepts only generated empty partition replay teardown"}
}

func wholeReplayMember(parent, child *catalog.Relation) bool {
	m := child.Membership
	if parent.Partition == nil || m == nil || m.Parent != parent.Address() || m.Strategy != parent.Partition.Strategy {
		return false
	}
	if m.Strategy == parser.PartitionHash {
		return !m.DefaultBound && m.Modulus == 1 && m.Remainder == 0
	}
	return m.DefaultBound
}

func emptyReplayLeaf(e *core.Engine, table *catalog.Relation) bool {
	if table.Partition != nil {
		return false
	}
	store := e.Storage.Relations[table.Address()]
	return store != nil && len(store.Versions) == 0
}

func replayExclusionIndex(view catalog.Read, table *catalog.Relation, c *catalog.ExclusionConstraint) *catalog.Relation {
	idx, ok := view.Relation(c.Index)
	if !ok || idx.SQLIndex == nil || !idx.SQLIndex.IsExclusion() || !idx.SQLIndex.Ready || !idx.SQLIndex.Live || idx.SQLIndex.Parent != table.Address() || idx.Owner != table.Owner || !index.BackingConstraintPresent(table, idx) {
		return nil
	}
	return idx
}

func replayExclusion(table *catalog.Relation, oid ids.OID) *catalog.ExclusionConstraint {
	for i := range table.Constraints.Exclusion {
		if table.Constraints.Exclusion[i].OID == oid {
			return &table.Constraints.Exclusion[i]
		}
	}
	return nil
}

func captureSeedParentLocked(e *core.Engine, view catalog.Read, parentRef, childRef ids.ObjectRef, parent, child *catalog.Relation) partition.ReplayTeardown {
	if parent.Partition == nil || len(parent.Partition.Children) != 1 || parent.Partition.Children[0] != child.Address() || !wholeReplayMember(parent, child) || len(parent.Constraints.Exclusion) == 0 {
		return partition.ReplayTeardown{}
	}
	record := partition.ReplayTeardown{Target: parentRef, Peer: childRef, Owner: parent.Owner, PeerOwner: child.Owner, Kind: partition.ReplaySeedParent}
	for i := range parent.Constraints.Exclusion {
		c := &parent.Constraints.Exclusion[i]
		parentIndex := replayExclusionIndex(view, parent, c)
		if parentIndex == nil || c.Parent != 0 || !c.IsLocal || !c.NoInherit || !strings.HasPrefix(c.Name, partition.ReplayStem) {
			return partition.ReplayTeardown{}
		}
		var inherited *catalog.ExclusionConstraint
		for j := range child.Constraints.Exclusion {
			candidate := &child.Constraints.Exclusion[j]
			if candidate.Parent == c.OID {
				if inherited != nil {
					return partition.ReplayTeardown{}
				}
				inherited = candidate
			}
		}
		if inherited == nil || inherited.IsLocal {
			return partition.ReplayTeardown{}
		}
		childIndex := replayExclusionIndex(view, child, inherited)
		if childIndex == nil || childIndex.SQLIndex.PartitionParent != parentIndex.Address() {
			return partition.ReplayTeardown{}
		}
		record.Backings = append(record.Backings, partition.ReplayTeardownBacking{Constraint: c.OID, Parent: inherited.OID, Index: e.Catalog.Domain.Ref(view.Epoch(), parentIndex.Address()), ParentIndex: e.Catalog.Domain.Ref(view.Epoch(), childIndex.Address()), NoInherit: inherited.NoInherit})
	}
	return record
}

// CapturePartitionReplayAfter completes a pre-detach capture or records the
// real invalid ONLY provider on the temporary child. Only successful ordinary
// DDL reaches this hook; nothing here repairs catalog state or executes DROP.
func CapturePartitionReplayAfter(e *core.Engine, op *session.Operation, stmt parser.Statement, records map[ids.ObjectAddress]partition.ReplayTeardown) error {
	defer func() {
		for address, record := range records {
			if record.Extension != nil {
				record.Extension = nil
				records[address] = record
			}
		}
	}()
	detach, detached := stmt.(*parser.AlterTableDetachPartition)
	alter, altered := stmt.(*parser.AlterTable)
	if !detached && !altered {
		return nil
	}
	if detached && (len(detach.Name.Parts) != 2 || len(detach.Partition.Parts) != 2) {
		return nil
	}
	if altered && (len(alter.Name.Parts) != 2 || len(alter.Actions) != 1 || alter.Actions[0].Action != parser.AddExclusion || alter.Actions[0].Exclusion == nil || !strings.HasPrefix(alter.Name.Parts[1].Text, partition.ReplayStem) || !strings.HasPrefix(alter.Actions[0].Exclusion.Name.Text, partition.ReplayStem)) {
		return nil
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	if e.ImportTargets == nil {
		return replayTeardownError()
	}
	view := auth.Catalog
	if detached {
		parent, err := e.ResolveRelationReference(auth, detach.Name, true)
		if err != nil {
			return err
		}
		child, err := e.ResolveRelationReference(auth, detach.Partition, true)
		if err != nil {
			return err
		}
		for address, record := range records {
			matches := record.Kind == partition.ReplaySeedParent && record.Target == parent && record.Peer == child || (record.Kind == partition.ReplayDetachedGap || record.Kind == partition.ReplayPromotionGap) && record.Target == child && record.Peer == parent
			if !matches {
				continue
			}
			record.Backings = slices.Clone(record.Backings)
			if !completeReplayCaptureLocked(e, view, &record) {
				return replayTeardownError()
			}
			records[address] = record
		}
		return nil
	}
	seedParent, err := e.ResolveRelationReference(auth, alter.Name, true)
	if err != nil {
		return err
	}
	if alter.Only {
		return captureInvalidChildLocked(e, view, seedParent, alter, records)
	}
	// Publish owned successors only after all affected extension deltas pass.
	var completed []partition.ReplayTeardown
	for _, record := range records {
		if record.Kind != partition.ReplayInvalidChild {
			continue
		}
		peer, ok := view.Relation(record.Peer.Address)
		if !ok || peer.Membership == nil || peer.Membership.Parent != seedParent.Address {
			continue
		}
		if !replayExtensionMatches(e, view, &record, alter, seedParent) || !completeInvalidCaptureLocked(e, view, seedParent, &record) {
			return replayTeardownError()
		}
		record.Extension = nil
		completed = append(completed, record)
	}
	for _, record := range completed {
		records[record.Target.Address] = record
	}
	return nil
}

func completeReplayCaptureLocked(e *core.Engine, view catalog.Read, record *partition.ReplayTeardown) bool {
	// Callers own Backings before this fallible snapshot; inventory is new.
	record.Indexes = nil
	target, ok := view.Relation(record.Target.Address)
	peer, live := view.Relation(record.Peer.Address)
	if !ok || !live || e.Catalog.Domain.Ref(view.Epoch(), target.Address()) != record.Target || e.Catalog.Domain.Ref(view.Epoch(), peer.Address()) != record.Peer || target.Owner != record.Owner || peer.Owner != record.PeerOwner {
		return false
	}
	record.TargetRevision, record.PeerRevision = target.Revision(), peer.Revision()
	record.PeerDefinition = peer
	if record.Gap.Domain() != nil {
		gap, ok := view.Relation(record.Gap.Address)
		if !ok || e.Catalog.Domain.Ref(view.Epoch(), gap.Address()) != record.Gap || gap.Owner != record.GapOwner {
			return false
		}
		record.GapRevision = gap.Revision()
	}
	for i := range record.Backings {
		b := &record.Backings[i]
		idx, ok := view.Relation(b.Index.Address)
		if !ok || e.Catalog.Domain.Ref(view.Epoch(), idx.Address()) != b.Index {
			return false
		}
		b.Revision = idx.Revision()
		if b.ParentIndex.Domain() == nil && record.Kind == partition.ReplayInvalidChild && b.Parent == 0 {
			continue
		}
		parent, live := view.Relation(b.ParentIndex.Address)
		if !live || e.Catalog.Domain.Ref(view.Epoch(), parent.Address()) != b.ParentIndex {
			return false
		}
		b.ParentRevision = parent.Revision()
	}
	for _, descriptor := range view.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if ok && idx.SQLIndex != nil && (idx.SQLIndex.Parent == target.Address() || record.Gap.Domain() != nil && idx.SQLIndex.Parent == record.Gap.Address || record.Kind == partition.ReplayPromotionGap && idx.SQLIndex.Parent == peer.Address()) {
			record.Indexes = append(record.Indexes, partition.ReplayTeardownIndex{Ref: e.Catalog.Domain.Ref(view.Epoch(), idx.Address()), Revision: idx.Revision()})
		}
	}
	record.Complete = true
	return true
}

func replayIndexesMatch(e *core.Engine, view catalog.Read, record *partition.ReplayTeardown) bool {
	count := 0
	for _, descriptor := range view.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Parent != record.Target.Address && (record.Gap.Domain() == nil || idx.SQLIndex.Parent != record.Gap.Address) && (record.Kind != partition.ReplayPromotionGap || idx.SQLIndex.Parent != record.Peer.Address) {
			continue
		}
		owner := record.Owner
		if idx.SQLIndex.Parent == record.Gap.Address {
			owner = record.GapOwner
		}
		if record.Kind == partition.ReplayPromotionGap && idx.SQLIndex.Parent == record.Peer.Address {
			owner = record.PeerOwner
		}
		if idx.Owner != owner {
			return false
		}
		found := false
		for _, saved := range record.Indexes {
			if saved.Ref == e.Catalog.Domain.Ref(view.Epoch(), idx.Address()) && saved.Revision == idx.Revision() {
				found = true
				break
			}
		}
		if !found {
			return false
		}
		count++
	}
	return count == len(record.Indexes)
}

func validateSeedParentLocked(e *core.Engine, view catalog.Read, target, peer *catalog.Relation, record *partition.ReplayTeardown) bool {
	if target.Partition == nil || target.Membership != nil || len(target.Partition.Children) != 0 || len(target.Constraints.Exclusion) != len(record.Backings) || len(record.Backings) == 0 || !replayIndexesMatch(e, view, record) || len(record.Indexes) != len(record.Backings) {
		return false
	}
	for i := range record.Backings {
		b := &record.Backings[i]
		seed, child := replayExclusion(target, b.Constraint), replayExclusion(peer, b.Parent)
		if seed == nil || child == nil || !seed.IsLocal || !seed.NoInherit || seed.Parent != 0 || seed.Index != b.Index.Address || !child.IsLocal || child.NoInherit != b.NoInherit || child.Parent != 0 || child.Index != b.ParentIndex.Address {
			return false
		}
		idx, childIndex := replayExclusionIndex(view, target, seed), replayExclusionIndex(view, peer, child)
		if idx == nil || childIndex == nil || e.Catalog.Domain.Ref(view.Epoch(), idx.Address()) != b.Index || e.Catalog.Domain.Ref(view.Epoch(), childIndex.Address()) != b.ParentIndex || idx.Revision() != b.Revision || childIndex.Revision() != b.ParentRevision || childIndex.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
			return false
		}
		// No remaining constraint or physical backing may cross the removed edge.
		for _, descriptor := range view.Descriptors() {
			rel, ok := descriptor.(*catalog.Relation)
			if !ok {
				continue
			}
			if rel.SQLIndex != nil && rel.SQLIndex.PartitionParent == idx.Address() {
				return false
			}
			for _, c := range rel.Constraints.Exclusion {
				if c.Parent == seed.OID {
					return false
				}
			}
			for _, c := range rel.Constraints.Unique {
				if c.Parent == seed.OID {
					return false
				}
			}
		}
	}
	return true
}

func captureInvalidChildLocked(e *core.Engine, view catalog.Read, ref ids.ObjectRef, raw *parser.AlterTable, records map[ids.ObjectAddress]partition.ReplayTeardown) error {
	target := view.MustRelation(ref.Address)
	if target.Partition == nil || len(target.Partition.Children) != 1 || target.Membership == nil {
		return replayTeardownError()
	}
	peer := view.MustRelation(target.Membership.Parent)
	gap := view.MustRelation(target.Partition.Children[0])
	if !strings.HasPrefix(gap.Name(), partition.ReplayStem) || gap.Partition != nil || !emptyReplayLeaf(e, gap) || !wholeReplayMember(peer, target) || !wholeReplayMember(target, gap) || peer.Partition == nil || len(peer.Partition.Children) != 1 {
		return replayTeardownError()
	}
	var constraint *catalog.ExclusionConstraint
	for i := range target.Constraints.Exclusion {
		c := &target.Constraints.Exclusion[i]
		if c.Name == raw.Actions[0].Exclusion.Name.Text {
			constraint = c
			break
		}
	}
	if constraint == nil || !constraint.NoInherit || !constraint.IsLocal || constraint.Parent != 0 {
		return replayTeardownError()
	}
	idx := replayExclusionIndex(view, target, constraint)
	if idx == nil || idx.SQLIndex.Valid || idx.SQLIndex.PartitionParent != (ids.ObjectAddress{}) || index.PartitionDumpIndexBacking(view, idx.Address(), gap.Address()) != nil {
		return replayTeardownError()
	}
	record, exists := records[ref.Address]
	peerRef, gapRef := e.Catalog.Domain.Ref(view.Epoch(), peer.Address()), e.Catalog.Domain.Ref(view.Epoch(), gap.Address())
	if exists {
		if !replayExtensionMatches(e, view, &record, raw, ref) {
			return replayTeardownError()
		}
		if record.Kind != partition.ReplayInvalidChild || record.Target != ref || record.Peer != peerRef || record.Gap != gapRef || record.Owner != target.Owner || record.PeerOwner != peer.Owner || record.GapOwner != gap.Owner {
			return replayTeardownError()
		}
		for _, b := range record.Backings {
			if b.Constraint == constraint.OID {
				return replayTeardownError()
			}
		}
	} else {
		record = partition.ReplayTeardown{Target: ref, Peer: peerRef, Gap: gapRef, Owner: target.Owner, PeerOwner: peer.Owner, GapOwner: gap.Owner, Kind: partition.ReplayInvalidChild}
	}
	record.Backings = append(slices.Clone(record.Backings), partition.ReplayTeardownBacking{Constraint: constraint.OID, Index: e.Catalog.Domain.Ref(view.Epoch(), idx.Address())})
	if !completeReplayCaptureLocked(e, view, &record) {
		return replayTeardownError()
	}
	record.Extension = nil
	record.Complete = false
	records[ref.Address] = record
	return nil
}

func completeInvalidCaptureLocked(e *core.Engine, view catalog.Read, seedParent ids.ObjectRef, record *partition.ReplayTeardown) bool {
	record.Backings = slices.Clone(record.Backings)
	target, ok := view.Relation(record.Target.Address)
	peer, live := view.Relation(record.Peer.Address)
	if !ok || !live || peer.Membership == nil || peer.Membership.Parent != seedParent.Address || e.Catalog.Domain.Ref(view.Epoch(), peer.Membership.Parent) != seedParent {
		return false
	}
	allAdopted := true
	for i := range record.Backings {
		b := &record.Backings[i]
		c := replayExclusion(target, b.Constraint)
		if c == nil || !c.NoInherit || c.Index != b.Index.Address {
			return false
		}
		if c.Parent == 0 {
			if !c.IsLocal || b.Parent != 0 {
				return false
			}
			allAdopted = false
			continue
		}
		if c.IsLocal {
			return false
		}
		parent := replayExclusion(peer, c.Parent)
		if parent == nil || parent.NoInherit || parent.IsLocal || parent.Parent == 0 {
			return false
		}
		idx, parentIndex := replayExclusionIndex(view, target, c), replayExclusionIndex(view, peer, parent)
		if idx == nil || parentIndex == nil || idx.SQLIndex.Valid || parentIndex.SQLIndex.Valid || idx.SQLIndex.PartitionParent != parentIndex.Address() {
			return false
		}
		seed, ok := view.Relation(parentIndex.SQLIndex.PartitionParent)
		if !ok || seed.SQLIndex == nil || seed.SQLIndex.Parent != seedParent.Address {
			return false
		}
		b.Parent, b.ParentIndex = parent.OID, e.Catalog.Domain.Ref(view.Epoch(), parentIndex.Address())
	}
	if !completeReplayCaptureLocked(e, view, record) {
		return false
	}
	record.Complete = allAdopted
	return true
}

func validateInvalidChildLocked(e *core.Engine, view catalog.Read, target, peer *catalog.Relation, record *partition.ReplayTeardown) bool {
	gap, ok := view.Relation(record.Gap.Address)
	if !ok || e.Catalog.Domain.Ref(view.Epoch(), gap.Address()) != record.Gap || gap.Owner != record.GapOwner || gap.Revision() != record.GapRevision || !emptyReplayLeaf(e, gap) || target.Partition == nil || len(target.Partition.Children) != 1 || target.Partition.Children[0] != gap.Address() || peer.Partition == nil || len(peer.Partition.Children) != 1 || peer.Partition.Children[0] != target.Address() || !wholeReplayMember(peer, target) || !wholeReplayMember(target, gap) || len(record.Backings) == 0 || !replayIndexesMatch(e, view, record) {
		return false
	}
	for i := range record.Backings {
		b := &record.Backings[i]
		c, parent := replayExclusion(target, b.Constraint), replayExclusion(peer, b.Parent)
		if c == nil || parent == nil || !c.NoInherit || c.IsLocal || c.Parent != parent.OID || c.Index != b.Index.Address || parent.NoInherit || parent.IsLocal || parent.Parent == 0 || parent.Index != b.ParentIndex.Address {
			return false
		}
		idx, parentIndex := replayExclusionIndex(view, target, c), replayExclusionIndex(view, peer, parent)
		if idx == nil || parentIndex == nil || idx.Revision() != b.Revision || parentIndex.Revision() != b.ParentRevision || e.Catalog.Domain.Ref(view.Epoch(), idx.Address()) != b.Index || e.Catalog.Domain.Ref(view.Epoch(), parentIndex.Address()) != b.ParentIndex || idx.SQLIndex.Valid || parentIndex.SQLIndex.Valid || idx.SQLIndex.PartitionParent != parentIndex.Address() || index.PartitionDumpIndexBacking(view, idx.Address(), gap.Address()) != nil {
			return false
		}
	}
	// All additional index providers must be actual inherited edges in this
	// captured two-level scope, never unrelated local or foreign providers.
	for _, saved := range record.Indexes {
		idx := view.MustRelation(saved.Ref.Address)
		parent, ok := view.Relation(idx.SQLIndex.PartitionParent)
		expected := peer.Address()
		if idx.SQLIndex.Parent == gap.Address() {
			expected = target.Address()
		}
		if !ok || parent.SQLIndex == nil || parent.SQLIndex.Parent != expected || !index.BackingConstraintPresent(view.MustRelation(idx.SQLIndex.Parent), idx) {
			return false
		}
	}
	return true
}

func promotionReplayGap(view catalog.Read, parent, child *catalog.Relation) bool {
	m := child.Membership
	if parent.Partition == nil || len(parent.Partition.Children) < 2 || parent.Partition.Strategy != parser.PartitionRange || m == nil || m.Parent != parent.Address() || m.DefaultBound || m.Strategy != parser.PartitionRange || len(m.Lower) != len(parent.Partition.Keys) || len(m.Upper) != len(parent.Partition.Keys) {
		return false
	}
	for i := range m.Lower {
		if m.Lower[i].Kind != -1 || m.Upper[i].Kind != 1 {
			return false
		}
	}
	return promotionReplayParent(view, parent, child.Address())
}

func promotionReplayParent(view catalog.Read, parent *catalog.Relation, gap ids.ObjectAddress) bool {
	for i := range parent.Constraints.Exclusion {
		c := &parent.Constraints.Exclusion[i]
		idx := replayExclusionIndex(view, parent, c)
		if idx == nil || idx.SQLIndex.Valid {
			continue
		}
		covered := 0
		valid := true
		for _, address := range parent.Partition.Children {
			backing := index.PartitionDumpIndexBacking(view, idx.Address(), address)
			if address == gap {
				valid = valid && backing == nil
				continue
			}
			if backing == nil || !backing.SQLIndex.Valid || !index.BackingConstraintPresent(view.MustRelation(address), backing) {
				valid = false
				break
			}
			covered++
		}
		if valid && covered != 0 {
			return true
		}
	}
	return false
}
