package visibility

import (
	"stuntgres/internal/engine/ids"
)

// CaptureSerializableLocked marks ref's enrolled node captured at the current
// commit frontier and finalization order. A read-only node collects the
// overlapping read-write writers it must wait out; with none it is exempt
// at once, otherwise it gets a ready channel that resolution closes. Caller
// holds Engine.mu and has enrolled ref.
func (reg *Registry) CaptureSerializableLocked(ref ids.TxRef, readOnly, deferrable bool) *SerializableTx {
	node := reg.Serializable.Nodes[ref]
	node.Captured = true
	node.ReadOnly = readOnly
	node.Deferrable = deferrable
	node.Frontier = reg.commitSeq
	node.SnapshotOrder = reg.Serializable.order
	node.Resolved = false
	node.Unsafe = false
	node.Exempt = false
	node.Possible = nil
	node.Ready = nil
	if node.ReadOnly {
		for _, writer := range reg.Serializable.Nodes {
			if writer != node && serializableRelevant(writer) && !serializableReadOnly(writer) && !writer.Committed && writer.Ref.Epoch() == node.Ref.Epoch() {
				if node.Possible == nil {
					node.Possible = make(map[*SerializableTx]struct{})
				}
				node.Possible[writer] = struct{}{}
			}
		}
		if len(node.Possible) == 0 {
			node.Resolved = true
			node.Exempt = true
		} else {
			node.Ready = make(chan struct{})
		}
	}
	return node
}

// SafeSnapshotWait returns the channel that a DEFERRABLE read-only capture
// waits on, and whether it must wait at all. Caller holds Engine.mu.
func (n *SerializableTx) SafeSnapshotWait() (<-chan struct{}, bool) {
	if !n.ReadOnly || !n.Deferrable || n.Resolved {
		return nil, false
	}
	return n.Ready, true
}

// SafeSnapshotOutcome reports, after a wait, whether the node is still live
// and whether its snapshot resolved safe. Caller holds Engine.mu.
func (n *SerializableTx) SafeSnapshotOutcome() (live, safe bool) {
	return !n.Aborted && !n.Ended, n.Resolved && !n.Unsafe
}

// AbandonSafeSnapshotLocked drops a failed acquisition's candidate writers and
// resolves it, closing its own ready channel. Caller holds Engine.mu.
func (n *SerializableTx) AbandonSafeSnapshotLocked() {
	n.Possible = nil
	if !n.Resolved {
		n.Resolved = true
		close(n.Ready)
	}
	n.Captured = false
	n.Ready = nil
}

// SafeSnapshotBlocker is a live read-write transaction that a DEFERRABLE
// safe-snapshot acquisition still waits out.
type SafeSnapshotBlocker struct {
	Session  ids.SessionToken
	Prepared bool
}

// SafeSnapshotBlockersLocked lists the writers blocking session's unresolved
// DEFERRABLE acquisition in domain and epoch, or nil when it has none.
// Caller holds Engine.mu.
func (reg *Registry) SafeSnapshotBlockersLocked(domain *ids.Domain, epoch ids.CatalogEpoch, session ids.SessionToken) []SafeSnapshotBlocker {
	for ref, node := range reg.Serializable.Nodes {
		if ref.Domain() != domain || ref.Epoch() != epoch || ref.Session() != session || node.Prepared || !node.Captured || !node.ReadOnly || !node.Deferrable || node.Aborted || node.Ended || node.Committed || node.Resolved || node.Ready == nil {
			continue
		}
		var blockers []SafeSnapshotBlocker
		for writer := range node.Possible {
			if writer.Ref.Domain() != domain || writer.Ref.Epoch() != ref.Epoch() || reg.Serializable.Nodes[writer.Ref] != writer || writer.Aborted || writer.Ended || writer.Committed {
				continue
			}
			blockers = append(blockers, SafeSnapshotBlocker{Session: writer.Ref.Session(), Prepared: writer.Prepared})
		}
		return blockers
	}
	return nil
}
