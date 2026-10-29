package partitioncmds

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// CapturePartitionReplayTeardown captures exact generated identities before an
// ordinary detach. The successful-execution hook completes its revision fence.
// A prefix alone never authorizes DROP, and target data is never waived.
func CapturePartitionReplayTeardown(e *core.Engine, op *session.Operation, s *parser.AlterTableDetachPartition) (partition.ReplayTeardown, error) {
	if len(s.Name.Parts) != 2 || len(s.Partition.Parts) != 2 || !strings.HasPrefix(s.Name.Parts[1].Text, partition.ReplayStem) && !strings.HasPrefix(s.Partition.Parts[1].Text, partition.ReplayStem) {
		return partition.ReplayTeardown{}, nil
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return partition.ReplayTeardown{}, err
	}
	if e.ImportTargets == nil {
		return partition.ReplayTeardown{}, fmt.Errorf("stuntgres: invalid private partition replay")
	}
	parent, err := e.ResolveRelationReference(auth, s.Name, true)
	if err != nil {
		return partition.ReplayTeardown{}, err
	}
	child, err := e.ResolveRelationReference(auth, s.Partition, true)
	if err != nil {
		return partition.ReplayTeardown{}, err
	}
	p := auth.Catalog.MustRelation(parent.Address)
	c := auth.Catalog.MustRelation(child.Address)
	if strings.HasPrefix(p.Name(), partition.ReplayStem) {
		if record := captureSeedParentLocked(e, auth.Catalog, parent, child, p, c); record.Kind != 0 {
			return record, nil
		}
	}
	kind := partition.ReplayDetachedGap
	if promotionReplayGap(auth.Catalog, p, c) {
		kind = partition.ReplayPromotionGap
	}
	if !strings.HasPrefix(c.Name(), partition.ReplayStem) || p.Partition == nil || kind == partition.ReplayDetachedGap && (len(p.Partition.Children) != 1 || !wholeReplayMember(p, c)) || c.Partition != nil {
		return partition.ReplayTeardown{}, nil
	}
	store := e.Storage.Relations[child.Address]
	if store == nil || len(store.Versions) != 0 {
		return partition.ReplayTeardown{}, nil
	}
	return partition.ReplayTeardown{Target: child, Peer: parent, Owner: c.Owner, PeerOwner: p.Owner, Kind: kind}, nil
}

// ValidatePartitionReplayTeardown consumes only a completed current capture.
// Successful admission still executes ordinary dependency-aware DROP TABLE.
func ValidatePartitionReplayTeardown(e *core.Engine, op *session.Operation, name parser.ObjectName, records map[ids.ObjectAddress]partition.ReplayTeardown) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	ref, err := e.ResolveRelationReference(auth, name, true)
	if err != nil {
		return err
	}
	record, ok := records[ref.Address]
	if !ok || record.Target != ref || e.ImportTargets == nil || !record.Complete {
		return replayTeardownError()
	}
	target, ok := auth.Catalog.Relation(ref.Address)
	peer, live := auth.Catalog.Relation(record.Peer.Address)
	if !ok || !live || e.Catalog.Domain.Ref(auth.Catalog.Epoch(), peer.Address()) != record.Peer || target.Owner != record.Owner || peer.Owner != record.PeerOwner || target.Revision() != record.TargetRevision || peer.Revision() != record.PeerRevision {
		return replayTeardownError()
	}
	valid := false
	switch record.Kind {
	case partition.ReplayDetachedGap:
		if target.Membership != nil || target.Partition != nil || peer.Partition == nil || len(peer.Partition.Children) != 0 || !emptyReplayLeaf(e, target) {
			break
		}
		for _, descriptor := range auth.Catalog.Descriptors() {
			idx, ok := descriptor.(*catalog.Relation)
			if ok && idx.SQLIndex != nil && idx.SQLIndex.Parent == peer.Address() && !idx.SQLIndex.Valid && idx.SQLIndex.Ready && idx.SQLIndex.Live {
				valid = true
				break
			}
		}
	case partition.ReplayPromotionGap:
		valid = target.Membership == nil && target.Partition == nil && peer.Partition != nil && len(peer.Partition.Children) != 0 && emptyReplayLeaf(e, target) && replayIndexesMatch(e, auth.Catalog, &record) && promotionReplayParent(auth.Catalog, peer, ids.ObjectAddress{})
	case partition.ReplaySeedParent:
		valid = validateSeedParentLocked(e, auth.Catalog, target, peer, &record)
	case partition.ReplayInvalidChild:
		valid = validateInvalidChildLocked(e, auth.Catalog, target, peer, &record)
	}
	if !valid {
		return replayTeardownError()
	}
	delete(records, ref.Address)
	return nil
}
