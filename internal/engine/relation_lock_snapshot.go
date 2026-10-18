package engine

import (
	"fmt"
	"math"
	"time"

	"stuntgres/internal/engine/ids"
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
	Session  ids.SessionID
	Local    ids.TxID
	Prepared bool
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
	Kind          pgLockKind
	Owner         pgLockOwner
	Mode          relationLockMode
	Object, Class uint32
	Subobject     int16
	Target        pgLockOwner
	XID           uint32
	Waiting       bool
	WaitStart     time.Time
}

func publicLockOwner(domain *ids.Domain, ref ids.TxRef, prepared bool) (pgLockOwner, bool, error) {
	token := ref.Session()
	if ref.Domain() != domain || token.Domain() != domain || token.Private() != nil || token.ID() == 0 {
		return pgLockOwner{}, false, nil
	}
	if !prepared && token.ID() > math.MaxInt32 {
		return pgLockOwner{}, false, fmt.Errorf("stuntgres: lock session identity exceeds integer range")
	}
	return pgLockOwner{Session: token.ID(), Local: ref.ID(), Prepared: prepared}, true, nil
}

func (m *relationLockManager) pgLockOwner(t *relationTicket) (pgLockOwner, bool, error) {
	if !m.valid(t) || t.persistent {
		return pgLockOwner{}, false, nil
	}
	return publicLockOwner(m.domain, t.ref, t.detached)
}

// Capture only: no settling, deadlock/timeout evaluation, callbacks, lazy
// completion registration or Engine/Session/TX synchronization is permitted.
func (m *relationLockManager) PGLocks(epoch ids.CatalogEpoch) ([]pgLockRecord, error) {
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
	var active map[ids.SessionID]pgLockOwner
	for _, ticket := range m.owners {
		if ticket.ref.Epoch() != epoch {
			continue
		}
		owner, visible, err := m.pgLockOwner(ticket)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		identity := pgLockIdentity{owner: owner, xid: ticket.AssignedPublicXID()}
		identities[ticket] = identity
		if !ticket.detached {
			records = append(records, pgLockRecord{Kind: pgVirtualLock, Owner: owner, Target: owner})
			if session := m.sessions[ticket.session]; session != nil && len(session.resources) != 0 {
				if active == nil {
					active = make(map[ids.SessionID]pgLockOwner)
				}
				if active[owner.Session].Local < owner.Local {
					active[owner.Session] = owner
				}
			}
		}
		if identity.xid != 0 {
			records = append(records, pgLockRecord{Kind: pgTransactionLock, Owner: owner, XID: identity.xid})
		}
	}
	// Reuse one resource-local index; live lifetimes contribute modes to one
	// process/object row, while detached prepared participants stay separate.
	var advisoryRows map[pgLockOwner]int
	for resource, entry := range m.entries {
		if resource.Kind == ordinaryResource && resource.completion == nil && resource.Epoch != epoch {
			continue
		}
		// Logical row/key holds have no physical target. Completion ownership
		// was emitted exactly once from the owning ticket above.
		projectHolds := resource.Row == 0 && resource.key == nil && resource.completion == nil
		advisory := resource.Kind == advisoryBigintResource || resource.Kind == advisoryPairResource
		clear(advisoryRows)
		if projectHolds {
			for ticket, hold := range entry.holders {
				if hold.Mode == 0 || !m.valid(ticket) {
					continue
				}
				var owner pgLockOwner
				var visible bool
				var err error
				if ticket.persistent {
					owner, visible, err = publicLockOwner(m.domain, m.domain.Tx(ticket.session, 0, 0, nil, 0), false)
					if current, ok := active[owner.Session]; ok {
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
						if current, ok := active[owner.Session]; ok {
							owner = current
						}
					}
					if index, ok := advisoryRows[owner]; ok {
						records[index].Mode |= hold.Mode
						continue
					}
					if advisoryRows == nil {
						advisoryRows = make(map[pgLockOwner]int)
					}
					advisoryRows[owner] = len(records)
				}
				record, err := pgResourceRecord(resource, owner, hold.Mode)
				if err != nil {
					return nil, err
				}
				records = append(records, record)
			}
		}
		for _, request := range entry.pending {
			if !m.validInquiryRequest(request, entry, resource) || request.provenance.Epoch() != epoch {
				continue
			}
			identity, visible := identities[m.owners[request.provenance]]
			if !visible {
				continue
			}
			owner := identity.owner
			var record pgLockRecord
			if resource.completion != nil || resource.Row != 0 || resource.key != nil {
				target := resource.completion
				purpose := request.policy.CompletionPurpose
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
				record = pgLockRecord{Kind: pgVirtualLock, Owner: owner, Target: targetIdentity.owner}
				if (purpose == completionTransaction || targetIdentity.owner.Prepared) && targetIdentity.xid != 0 {
					record.Kind, record.XID = pgTransactionLock, targetIdentity.xid
				} else if targetIdentity.owner.Prepared {
					return nil, fmt.Errorf("stuntgres: lock dependency lacks live transaction identity")
				}
			} else {
				var err error
				record, err = pgResourceRecord(resource, owner, request.mode)
				if err != nil {
					return nil, err
				}
			}
			record.Waiting, record.WaitStart = true, request.enqueuedAt
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
		if !m.valid(ticket) || sameLockSession(ticket, request.ticket) || !Conflicts(request.mode, hold.Mode) {
			continue
		}
		if target == nil || ticket.ref.Diagnostic() < target.ref.Diagnostic() {
			target = ticket
		}
	}
	if target != nil {
		return target
	}
	if request.ref.Row != 0 && request.mode == noKeyUpdate {
		return nil
	}
	for _, earlier := range request.entry.pending {
		if earlier == request {
			break
		}
		if m.validInquiryRequest(earlier, request.entry, request.ref) && !sameLockSession(earlier.ticket, request.ticket) && Conflicts(request.mode, earlier.mode) {
			return earlier.ticket
		}
	}
	return nil
}

func pgResourceRecord(resource lockResource, owner pgLockOwner, mode relationLockMode) (pgLockRecord, error) {
	r := pgLockRecord{Owner: owner, Mode: mode}
	switch resource.Kind {
	case advisoryBigintResource, advisoryPairResource:
		r.Kind, r.Class, r.Object, r.Subobject = pgAdvisoryLock, resource.High, resource.Low, 1
		if resource.Kind == advisoryPairResource {
			r.Subobject = 2
		}
	case ordinaryResource:
		r.Object = uint32(resource.Address.ObjectID)
		switch resource.Address.ClassID {
		case ids.RelationClassID:
			r.Kind = pgRelationLock
		case ids.TypeClassID:
			r.Kind, r.Class = pgObjectLock, uint32(ids.TypeClassID)
		default:
			return pgLockRecord{}, fmt.Errorf("stuntgres: unknown lock object class")
		}
	default:
		return pgLockRecord{}, fmt.Errorf("stuntgres: unknown lock resource kind")
	}
	return r, nil
}
