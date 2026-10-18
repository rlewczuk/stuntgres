package engine

import (
	"fmt"
	"math"
	"time"
)

type pgLockKind uint8

const (
	pgRelationLock pgLockKind = iota
	pgObjectLock
	pgAdvisoryLock
	pgVirtualLock
	pgTransactionLock
	pgPredicateLock
)

// These records contain only owned scalars. In particular, no ticket, resource
// key, queue, map, provider value or session pointer escapes manager metadata.
// The public view joins this manager capture with a separate SSI capture.
type pgLockOwner struct {
	session  SessionID
	local    TxID
	prepared bool
}

// Manager membership is stable while capturing, but canonical assignment and
// phase retirement update the atomic XID under Engine.mu. Sample that identity
// once, then use the same value for ownership and every dependency in this view.
// This lookup is capture-local; no ticket pointer escapes in result records.
type pgLockIdentity struct {
	owner pgLockOwner
	xid   uint32
}

type pgLockRecord struct {
	kind          pgLockKind
	owner         pgLockOwner
	mode          relationLockMode
	object, class uint32
	subobject     int16
	target        pgLockOwner
	xid           uint32
	waiting       bool
	waitstart     time.Time
}

func publicLockOwner(domain *Domain, ref TxRef, prepared bool) (pgLockOwner, bool, error) {
	token := ref.session
	if ref.domain != domain || token.domain != domain || privateSessionOf(token) != nil || token.id == 0 {
		return pgLockOwner{}, false, nil
	}
	if !prepared && token.id > math.MaxInt32 {
		return pgLockOwner{}, false, fmt.Errorf("stuntgres: lock session identity exceeds integer range")
	}
	return pgLockOwner{session: token.id, local: ref.id, prepared: prepared}, true, nil
}

func (m *relationLockManager) pgLockOwner(t *relationTicket) (pgLockOwner, bool, error) {
	if !m.valid(t) || t.persistent {
		return pgLockOwner{}, false, nil
	}
	return publicLockOwner(m.domain, t.ref, t.detached)
}

// Capture only: no settling, deadlock/timeout evaluation, callbacks, lazy
// completion registration or Engine/Session/TX synchronization is permitted.
func (m *relationLockManager) snapshotPGLocks(epoch CatalogEpoch) ([]pgLockRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.epoch > epoch {
		return nil, ErrCatalogReplaced
	}
	var records []pgLockRecord
	identities := make(map[*relationTicket]pgLockIdentity, len(m.owners))
	// Session advisory grants use the current incarnation, or its real idle /0
	// association. Build this lookup once rather than scanning every owner for
	// each advisory resource. Prospective CHAIN registrations use their newest
	// registered incarnation, whose VXID is already genuinely owned.
	var active map[SessionID]pgLockOwner
	for _, ticket := range m.owners {
		if ticket.ref.epoch != epoch {
			continue
		}
		owner, visible, err := m.pgLockOwner(ticket)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		identity := pgLockIdentity{owner: owner, xid: ticket.assignedPublicXID()}
		identities[ticket] = identity
		if !ticket.detached {
			records = append(records, pgLockRecord{kind: pgVirtualLock, owner: owner, target: owner})
			if session := m.sessions[ticket.session]; session != nil && len(session.resources) != 0 {
				if active == nil {
					active = make(map[SessionID]pgLockOwner)
				}
				if active[owner.session].local < owner.local {
					active[owner.session] = owner
				}
			}
		}
		if identity.xid != 0 {
			records = append(records, pgLockRecord{kind: pgTransactionLock, owner: owner, xid: identity.xid})
		}
	}
	// Reuse one resource-local index; live lifetimes contribute modes to one
	// process/object row, while detached prepared participants stay separate.
	var advisoryRows map[pgLockOwner]int
	for resource, entry := range m.entries {
		if resource.kind == ordinaryResource && resource.completion == nil && resource.Epoch != epoch {
			continue
		}
		// Logical row/key holds have no physical target. Completion ownership
		// was emitted exactly once from the owning ticket above.
		projectHolds := resource.row == 0 && resource.key == nil && resource.completion == nil
		advisory := resource.kind == advisoryBigintResource || resource.kind == advisoryPairResource
		clear(advisoryRows)
		if projectHolds {
			for ticket, hold := range entry.holders {
				if hold.mode == 0 || !m.valid(ticket) {
					continue
				}
				var owner pgLockOwner
				var visible bool
				var err error
				if ticket.persistent {
					owner, visible, err = publicLockOwner(m.domain, TxRef{domain: m.domain, session: ticket.session}, false)
					if current, ok := active[owner.session]; ok {
						owner = current
					}
				} else {
					identity, present := identities[ticket]
					owner, visible = identity.owner, present
				}
				if err != nil {
					return nil, err
				}
				if !visible {
					continue
				}
				if advisory {
					if !ticket.detached {
						if current, ok := active[owner.session]; ok {
							owner = current
						}
					}
					if index, ok := advisoryRows[owner]; ok {
						records[index].mode |= hold.mode
						continue
					}
					if advisoryRows == nil {
						advisoryRows = make(map[pgLockOwner]int)
					}
					advisoryRows[owner] = len(records)
				}
				record, err := pgResourceRecord(resource, owner, hold.mode)
				if err != nil {
					return nil, err
				}
				records = append(records, record)
			}
		}
		for _, request := range entry.pending {
			if !m.validInquiryRequest(request, entry, resource) || request.provenance.epoch != epoch {
				continue
			}
			identity, visible := identities[m.owners[request.provenance]]
			if !visible {
				continue
			}
			owner := identity.owner
			var record pgLockRecord
			if resource.completion != nil || resource.row != 0 || resource.key != nil {
				target := resource.completion
				purpose := request.policy.completionPurpose
				if target == nil {
					target = m.pgWaitDependency(request)
					purpose = completionTransaction
				}
				if target == nil {
					continue
				}
				targetIdentity, present := identities[target]
				if !present {
					continue
				}
				record = pgLockRecord{kind: pgVirtualLock, owner: owner, target: targetIdentity.owner}
				if (purpose == completionTransaction || targetIdentity.owner.prepared) && targetIdentity.xid != 0 {
					record.kind, record.xid = pgTransactionLock, targetIdentity.xid
				} else if targetIdentity.owner.prepared {
					return nil, fmt.Errorf("stuntgres: lock dependency lacks live transaction identity")
				}
			} else {
				var err error
				record, err = pgResourceRecord(resource, owner, request.mode)
				if err != nil {
					return nil, err
				}
			}
			record.waiting, record.waitstart = true, request.enqueuedAt
			records = append(records, record)
		}
	}
	return records, nil
}

// Select one real current edge. Holder preference and a deterministic diagnostic
// tie-break do not modify the graph or turn diagnostic identities into XIDs.
func (m *relationLockManager) pgWaitDependency(request *relationRequest) *relationTicket {
	var target *relationTicket
	for ticket, hold := range request.entry.holders {
		if !m.valid(ticket) || sameLockSession(ticket, request.ticket) || !lockConflict(request.mode, hold.mode) {
			continue
		}
		if target == nil || ticket.ref.diagnostic < target.ref.diagnostic {
			target = ticket
		}
	}
	if target != nil {
		return target
	}
	if request.ref.row != 0 && request.mode == noKeyUpdate {
		return nil
	}
	for _, earlier := range request.entry.pending {
		if earlier == request {
			break
		}
		if m.validInquiryRequest(earlier, request.entry, request.ref) && !sameLockSession(earlier.ticket, request.ticket) && lockConflict(request.mode, earlier.mode) {
			return earlier.ticket
		}
	}
	return nil
}

func pgResourceRecord(resource lockResource, owner pgLockOwner, mode relationLockMode) (pgLockRecord, error) {
	r := pgLockRecord{owner: owner, mode: mode}
	switch resource.kind {
	case advisoryBigintResource, advisoryPairResource:
		r.kind, r.class, r.object, r.subobject = pgAdvisoryLock, resource.high, resource.low, 1
		if resource.kind == advisoryPairResource {
			r.subobject = 2
		}
	case ordinaryResource:
		r.object = uint32(resource.Address.ObjectID)
		switch resource.Address.ClassID {
		case RelationClassID:
			r.kind = pgRelationLock
		case TypeClassID:
			r.kind, r.class = pgObjectLock, uint32(TypeClassID)
		default:
			return pgLockRecord{}, fmt.Errorf("stuntgres: unknown lock object class")
		}
	default:
		return pgLockRecord{}, fmt.Errorf("stuntgres: unknown lock resource kind")
	}
	return r, nil
}
