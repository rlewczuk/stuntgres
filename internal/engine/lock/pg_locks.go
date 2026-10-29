package lock

import (
	"fmt"
	"math"
	"time"

	"stuntgres/internal/engine/ids"
)

type RecordKind uint8

const (
	RelationRecord RecordKind = iota
	ObjectRecord
	AdvisoryRecord
	VirtualRecord
	TransactionRecord
	PredicateRecord
)

// These records contain only owned scalars. In particular, no ticket, resource
// key, queue, map, provider value or session pointer escapes manager metadata.
// The public view joins this manager capture with a separate SSI capture.
type RecordOwner struct {
	Session  ids.SessionID
	Local    ids.TxID
	Prepared bool
}

// Manager membership is stable while capturing, but canonical assignment and
// phase retirement update the atomic XID under Engine.mu. Sample that identity
// once, then use the same value for ownership and every dependency in this view.
// This lookup is capture-local; no ticket pointer escapes in result records.
type pgLockIdentity struct {
	owner RecordOwner
	xid   uint32
}

type Record struct {
	Kind          RecordKind
	Shared        bool // Real cluster object; SQL database column is zero.
	Owner         RecordOwner
	Mode          Mode
	Object, Class uint32
	Subobject     int16
	Target        RecordOwner
	XID           uint32
	Waiting       bool
	WaitStart     time.Time
}

func PublicOwner(domain *ids.Domain, ref ids.TxRef, prepared bool) (RecordOwner, bool, error) {
	token := ref.Session()
	if ref.Domain() != domain || token.Domain() != domain || token.Private() != nil || token.ID() == 0 {
		return RecordOwner{}, false, nil
	}
	if !prepared && token.ID() > math.MaxInt32 {
		return RecordOwner{}, false, fmt.Errorf("stuntgres: lock session identity exceeds integer range")
	}
	return RecordOwner{Session: token.ID(), Local: ref.ID(), Prepared: prepared}, true, nil
}

func (m *Manager) pgLockOwner(t *Ticket) (RecordOwner, bool, error) {
	if !m.valid(t) || t.persistent {
		return RecordOwner{}, false, nil
	}
	return PublicOwner(t.ref.Domain(), t.ref, t.detached)
}

// Capture only: no settling, deadlock/timeout evaluation, callbacks, lazy
// completion registration or Engine/Session/TX synchronization is permitted.
func (m *Manager) PGLocks(domain *ids.Domain, epoch ids.CatalogEpoch) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state := m.domains[domain]; state == nil || state.epoch > epoch {
		return nil, ids.ErrCatalogReplaced
	}
	var records []Record
	identities := make(map[*Ticket]pgLockIdentity, len(m.owners))
	// Only shared-role participants extend the established database-local view.
	shared := make(map[*Ticket]bool)
	for resource, entry := range m.entries {
		if m.roleDomain == nil || resource.Domain() != m.roleDomain || resource.Kind != OrdinaryResource && resource.Kind != RoleSettingResource {
			continue
		}
		for ticket := range entry.holders {
			shared[ticket] = true
		}
		for _, request := range entry.pending {
			if m.validInquiryRequest(request, entry, resource) {
				shared[request.ticket] = true
			}
		}
	}
	// Session advisory grants use the current incarnation, or its real idle /0
	// association. Build this lookup once rather than scanning every owner for
	// each advisory resource. Prospective CHAIN registrations use their newest
	// registered incarnation, whose VXID is already genuinely owned.
	var active map[ids.SessionID]RecordOwner
	for _, ticket := range m.owners {
		if ticket.ref.Domain() == domain && ticket.ref.Epoch() != epoch || ticket.ref.Domain() != domain && !shared[ticket] {
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
			records = append(records, Record{Kind: VirtualRecord, Owner: owner, Target: owner})
			if session := m.sessions[ticket.session]; session != nil && len(session.resources) != 0 {
				if active == nil {
					active = make(map[ids.SessionID]RecordOwner)
				}
				if active[owner.Session].Local < owner.Local {
					active[owner.Session] = owner
				}
			}
		}
		if identity.xid != 0 {
			records = append(records, Record{Kind: TransactionRecord, Owner: owner, XID: identity.xid})
		}
	}
	// Reuse one resource-local index; live lifetimes contribute modes to one
	// process/object row, while detached prepared participants stay separate.
	var advisoryRows map[RecordOwner]int
	for resource, entry := range m.entries {
		// Exact binding claims have no physical PostgreSQL lock tag. Their
		// queued dependencies still participate in blocking/deadlock inquiry.
		if resource.Name != (ids.NameKey{}) && resource.Domain() != m.roleDomain &&
			resource.Name.ClassID != ids.StatisticExtClassID && resource.Name.ClassID != ids.CollationClassID && resource.Name.ClassID != ids.ConversionClassID && resource.Name.ClassID != ids.CastClassID {
			continue
		}
		catalogACL := resource.Kind == CatalogACLResource || resource.Kind == DefaultACLResource || resource.Kind == RoleSettingResource
		if (resource.Kind == OrdinaryResource || catalogACL) && resource.completion == nil && resource.Domain() != m.roleDomain && (resource.Domain() != domain || resource.Epoch != epoch) ||
			(resource.Kind == AdvisoryBigintResource || resource.Kind == AdvisoryPairResource) && resource.Database != domain {
			continue
		}
		// Logical row/key holds have no physical target. Completion ownership
		// was emitted exactly once from the owning ticket above.
		projectHolds := !catalogACL && resource.Row == 0 && resource.key == nil && resource.completion == nil && resource.Name == (ids.NameKey{}) && !resource.RoleTuple
		advisory := resource.Kind == AdvisoryBigintResource || resource.Kind == AdvisoryPairResource
		clear(advisoryRows)
		if projectHolds {
			for ticket, hold := range entry.holders {
				if hold.Mode == 0 || !m.valid(ticket) {
					continue
				}
				var owner RecordOwner
				var visible bool
				var err error
				if ticket.persistent {
					owner, visible, err = PublicOwner(domain, domain.Tx(ticket.session, 0, 0, nil, 0), false)
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
						advisoryRows = make(map[RecordOwner]int)
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
			if !m.validInquiryRequest(request, entry, resource) || request.provenance.Domain() == domain && request.provenance.Epoch() != epoch {
				continue
			}
			identity, visible := identities[m.owners[request.provenance]]
			if !visible {
				continue
			}
			owner := identity.owner
			var record Record
			if catalogACL || resource.completion != nil || resource.Row != 0 || resource.key != nil || resource.Name != (ids.NameKey{}) || resource.RoleTuple {
				target := resource.completion
				purpose := request.policy.CompletionPurpose
				if target == nil {
					target = m.pgWaitDependency(request)
					purpose = CompletionTransaction
				}
				if target == nil {
					continue
				}
				targetIdentity, present := identities[target]
				if !present {
					continue
				}
				if (resource.Name != (ids.NameKey{}) || resource.RoleTuple) && targetIdentity.xid == 0 {
					return nil, fmt.Errorf("stuntgres: catalog claim dependency lacks live transaction identity")
				}
				record = Record{Kind: VirtualRecord, Owner: owner, Target: targetIdentity.owner}
				if (purpose == CompletionTransaction || targetIdentity.owner.Prepared) && targetIdentity.xid != 0 {
					record.Kind, record.XID = TransactionRecord, targetIdentity.xid
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
func (m *Manager) pgWaitDependency(request *relationRequest) *Ticket {
	var target *Ticket
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
	if request.ref.Row != 0 && request.mode == ForNoKeyUpdate {
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

func pgResourceRecord(resource Resource, owner RecordOwner, mode Mode) (Record, error) {
	r := Record{Owner: owner, Mode: mode}
	switch resource.Kind {
	case AdvisoryBigintResource, AdvisoryPairResource:
		r.Kind, r.Class, r.Object, r.Subobject = AdvisoryRecord, resource.High, resource.Low, 1
		if resource.Kind == AdvisoryPairResource {
			r.Subobject = 2
		}
	case OrdinaryResource:
		r.Object = uint32(resource.Address.ObjectID)
		switch resource.Address.ClassID {
		case ids.RelationClassID:
			r.Kind = RelationRecord
		case ids.LanguageClassID, ids.SubscriptionClassID, ids.StatisticExtClassID, ids.TypeClassID, ids.NamespaceClassID, ids.RoleClassID, ids.CollationClassID, ids.ConversionClassID, ids.CastClassID, ids.ProcedureClassID, ids.AccessMethodClassID, ids.OperatorFamilyClassID, ids.OperatorClassCatalogID, ids.EventTriggerClassID, ids.TSTemplateClassID, ids.TSDictionaryClassID:
			r.Kind, r.Class = ObjectRecord, uint32(resource.Address.ClassID)
			r.Shared = resource.Address.ClassID == ids.RoleClassID || resource.Address.ClassID == ids.SubscriptionClassID
		case ids.TSConfigurationClassID, ids.TSParserClassID:
			r.Kind, r.Class = ObjectRecord, uint32(resource.Address.ClassID)
			r.Shared = resource.Address.ClassID == ids.RoleClassID
		default:
			return Record{}, fmt.Errorf("stuntgres: unknown lock object class")
		}
	default:
		return Record{}, fmt.Errorf("stuntgres: unknown lock resource kind")
	}
	return r, nil
}
