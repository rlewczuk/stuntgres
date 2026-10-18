package engine

import (
	"errors"
	"fmt"
	"slices"

	"stuntgres/internal/engine/ids"
)

// The existing manager arbitrates by RowID. History additionally identifies the
// exact immutable version: a successor copies original raw xmax, not its new action.
type multiTuple struct {
	lockResource
	Version ids.VersionID
}
type multiMode uint8

const (
	multiKeyShare multiMode = iota
	multiShare
	multiNoKeyLock
	multiUpdateLock
	multiNoKeyAction
	multiUpdateAction
)

func (m multiMode) Name() string {
	switch m {
	case multiKeyShare:
		return "keysh"
	case multiShare:
		return "sh"
	case multiNoKeyLock:
		return "fornokeyupd"
	case multiUpdateLock:
		return "forupd"
	case multiNoKeyAction:
		return "nokeyupd"
	case multiUpdateAction:
		return "upd"
	}
	return ""
}
func (m multiMode) updating() bool { return m >= multiNoKeyAction }
func (m multiMode) strength() uint8 {
	switch m {
	case multiKeyShare:
		return 0
	case multiShare:
		return 1
	case multiNoKeyLock, multiNoKeyAction:
		return 2
	default:
		return 3
	}
}
func multiLockMode(mode relationLockMode) (multiMode, error) {
	switch mode {
	case keyShare:
		return multiKeyShare, nil
	case rowShare:
		return multiShare, nil
	case noKeyUpdate:
		return multiNoKeyLock, nil
	case rowUpdate:
		return multiUpdateLock, nil
	}
	return 0, errors.New("stuntgres: invalid physical row lock mode")
}

type multiMember struct {
	XID   uint32
	Mode  multiMode
	owner *relationTicket
}
type multiRow struct {
	id               uint32
	members          []multiMember
	inheritedID      uint32
	inheritedMembers []multiMember // Original raw xmax, before the updating event.
}
type multiEvent struct {
	Tuple     multiTuple
	Successor ids.VersionID
	Mode      multiMode
}
type multiContribution struct {
	owner *relationTicket
	tuple multiTuple
}

func nextMultiID(id uint32) uint32 {
	id++
	if id == 0 {
		id = 1
	}
	return id
}
func sameMultiMembers(a, b []multiMember) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].XID != b[i].XID || a[i].Mode != b[i].Mode {
			return false
		}
	}
	return true
}
func canonicalMultiMembers(members []multiMember) {
	slices.SortFunc(members, func(a, b multiMember) int {
		if a.XID < b.XID {
			return -1
		}
		if a.XID > b.XID {
			return 1
		}
		return int(a.Mode) - int(b.Mode)
	})
}
func (m *relationLockManager) NextMultiXact() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.multiNext
}
func (m *relationLockManager) MultiXactMembers(owner *relationTicket, id uint32) ([]multiMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if owner != nil && (!m.valid(owner) || owner.OwnershipEnded()) {
		return nil, ErrCatalogReplaced
	}
	if id == 0 {
		return nil, sqlError("22023", "invalid MultiXactId: 0")
	}
	if members, ok := m.multiHistory[id]; ok {
		if owner != nil {
			owner.multiCache = rememberMultiXact(owner.multiCache, id)
		}
		return slices.Clone(members), nil
	}
	message := "does no longer exist"
	if int32(id-m.multiNext) >= 0 {
		message = "has not been created yet"
	}
	return nil, sqlError("XX000", fmt.Sprintf("MultiXactId %d %s -- apparent wraparound", id, message))
}
func (m *relationLockManager) MultiAge(owner *relationTicket, id uint32) (int32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(owner) || owner.OwnershipEnded() {
		return 0, ErrCatalogReplaced
	}
	return multiTransactionAge(m.multiNext, id), nil
}

func (m *relationLockManager) retireMultiXactsLocked() {
	clear(m.multiRows)
	clear(m.multiHistory)
	for _, owner := range m.owners {
		clear(owner.multiContributions)
		owner.multiCache = nil
	}
}

func (m *relationLockManager) validMultiTuple(owner *relationTicket, key multiTuple) bool {
	return key.Kind == ordinaryResource && key.Row != 0 && key.High == 0 && key.Low == 0 && key.Address.ClassID == ids.RelationClassID && key.Address.SubID == 0 && key.key == nil && key.completion == nil && key.Version != 0 && key.ObjectRef.Domain() == m.domain && key.Epoch == owner.ref.Epoch()
}

// No Engine/owner mutex is held. The whole batch is prepared privately before
// publishing any history, cache, contribution or counter change under manager.mu.
func (m *relationLockManager) PhysicalEvents(owner *relationTicket, events []multiEvent) error {
	if len(events) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(owner) || owner.OwnershipEnded() {
		return errRelationOwnerEnded
	}
	xid := owner.AssignedPublicXID()
	if xid < 3 {
		return errors.New("stuntgres: physical event without actual transaction assignment")
	}
	if len(events) == 1 && events[0].Successor == 0 {
		event := events[0]
		key := event.Tuple
		entry := m.entries[key.lockResource]
		old := m.multiRows[key]
		if m.validMultiTuple(owner, key) && event.Mode <= multiUpdateAction && entry != nil && entry.holders[owner].Mode&(keyShare|rowShare|noKeyUpdate|rowUpdate) != 0 {
			for _, member := range old.members {
				if member.owner != owner || member.XID != xid || owner.multiContributions[key]&(1<<member.Mode) == 0 {
					continue
				}
				mode := event.Mode
				if old.id == 0 && len(old.members) == 1 && mode == multiNoKeyAction && member.Mode.strength() > mode.strength() {
					mode = multiUpdateAction
				}
				if member.Mode == mode || !mode.updating() && member.Mode.strength() >= mode.strength() {
					if old.id != 0 {
						owner.multiCache = rememberMultiXact(owner.multiCache, old.id)
					}
					return nil
				}
			}
		}
	}
	rows := make(map[multiTuple]multiRow, len(events)*2)
	contributions := make(map[multiContribution]uint8, len(events)*2)
	getContribution := func(t *relationTicket, key multiTuple) uint8 {
		c := multiContribution{t, key}
		if bits, ok := contributions[c]; ok {
			return bits
		}
		return t.multiContributions[key]
	}
	live := func(member multiMember, key multiTuple) bool {
		return member.owner != nil && !member.owner.OwnershipEnded() && member.owner.CompletionState() != ids.TxAborted && getContribution(member.owner, key)&(1<<member.Mode) != 0
	}
	getRow := func(key multiTuple) multiRow {
		if row, ok := rows[key]; ok {
			return row
		}
		return m.multiRows[key]
	}
	next := m.multiNext
	var records map[uint32][]multiMember
	cache := owner.multiCache
	privateCache := false
	remember := func(id uint32) {
		if len(cache) != 0 && cache[0] == id {
			return
		}
		if !privateCache {
			cache = slices.Clone(cache)
			privateCache = true
		}
		cache = rememberMultiXact(cache, id)
	}
	intern := func(members []multiMember) (uint32, error) {
		for _, id := range cache {
			cached := m.multiHistory[id]
			if cached == nil {
				cached = records[id]
			}
			if sameMultiMembers(cached, members) {
				remember(id)
				return id, nil
			}
		}
		if _, exists := m.multiHistory[next]; exists {
			return 0, errors.New("stuntgres: retained MultiXact identity collision")
		}
		if _, exists := records[next]; exists {
			return 0, errors.New("stuntgres: MultiXact identity exhausted")
		}
		id := next
		next = nextMultiID(next)
		if records == nil {
			records = make(map[uint32][]multiMember)
		}
		records[id] = members
		remember(id)
		return id, nil
	}
	for _, event := range events {
		key := event.Tuple
		if !m.validMultiTuple(owner, key) || event.Mode > multiUpdateAction || event.Successor != 0 && !event.Mode.updating() {
			return errors.New("stuntgres: invalid physical MultiXact event")
		}
		entry := m.entries[key.lockResource]
		if entry == nil || entry.holders[owner].Mode&(keyShare|rowShare|noKeyUpdate|rowUpdate) == 0 {
			return errors.New("stuntgres: physical event lacks genuine row grant")
		}
		old := getRow(key)
		if old.id != 0 {
			remember(old.id)
		}
		mode := event.Mode
		if old.id == 0 && len(old.members) == 1 && old.members[0].owner == owner && old.members[0].XID == xid && live(old.members[0], key) && mode == multiNoKeyAction && old.members[0].Mode.strength() > mode.strength() {
			mode = multiUpdateAction
		}
		if !mode.updating() {
			sufficient := false
			for _, member := range old.members {
				if member.owner == owner && member.XID == xid && live(member, key) && member.Mode.strength() >= mode.strength() {
					sufficient = true
					break
				}
			}
			if sufficient {
				continue
			}
		}
		pair := multiMember{xid, mode, owner}
		same := false
		for _, member := range old.members {
			if member.owner == owner && member.XID == xid && member.Mode == mode && live(member, key) {
				same = true
				break
			}
		}
		if !same {
			members := make([]multiMember, 0, len(old.members)+1)
			for _, member := range old.members {
				if live(member, key) || member.Mode.updating() && member.owner != nil && member.owner.CompletionState() == ids.TxCommitted {
					members = append(members, member)
				}
			}
			if old.id == 0 && len(members) == 1 && members[0].owner == owner {
				contributions[multiContribution{owner, key}] = 0
				members = members[:0]
			}
			members = append(members, pair)
			updating := 0
			for _, member := range members {
				if member.Mode.updating() {
					updating++
				}
			}
			if updating > 1 {
				return errors.New("stuntgres: multiple updating MultiXact members")
			}
			canonicalMultiMembers(members)
			row := multiRow{members: members}
			if mode.updating() {
				row.inheritedID, row.inheritedMembers = old.id, old.members
			}
			if len(members) > 1 {
				id, err := intern(members)
				if err != nil {
					return err
				}
				row.id = id
			}
			rows[key] = row
		}
		contribution := multiContribution{owner, key}
		contributions[contribution] = getContribution(owner, key) | (1 << pair.Mode)
		if event.Successor != 0 {
			successor := key
			successor.Version = event.Successor
			action := getRow(key)
			// heap_update copies the ORIGINAL raw xmax, not the computed action ID
			// or a live-only/strongest-mode reconstruction. Old records stay immutable.
			inherited := multiRow{id: action.inheritedID, members: action.inheritedMembers}
			rows[successor] = inherited
			for _, member := range inherited.members {
				running := live(member, key)
				if member.owner == owner && !owner.OwnershipEnded() && owner.CompletionState() != ids.TxAborted {
					running = true
				}
				if running {
					c := multiContribution{member.owner, successor}
					contributions[c] = getContribution(member.owner, successor) | (1 << member.Mode)
				}
			}
		}
	}
	if m.multiRows == nil {
		m.multiRows = make(map[multiTuple]multiRow)
	}
	if len(records) != 0 && m.multiHistory == nil {
		m.multiHistory = make(map[uint32][]multiMember)
	}
	for key, row := range rows {
		if len(row.members) == 0 {
			delete(m.multiRows, key)
		} else {
			m.multiRows[key] = row
		}
	}
	for c, bits := range contributions {
		if c.owner.multiContributions == nil {
			c.owner.multiContributions = make(map[multiTuple]uint8)
		}
		c.owner.multiContributions[c.tuple] = bits
	}
	for id, members := range records {
		m.multiHistory[id] = members
	}
	if privateCache {
		owner.multiCache = cache
	}
	m.multiNext = next
	return nil
}

// Restore contribution provenance independently of immutable retained records.
// Singleton current state can be restored without changing any allocated bag.
func (m *relationLockManager) restoreMultiContributionsLocked(t *relationTicket, held relationLockCheckpoint) {
	for tuple, bits := range t.multiContributions {
		saved := held[tuple.lockResource]
		var granted relationLockMode
		if entry := m.entries[tuple.lockResource]; entry != nil {
			granted = entry.holders[t].Mode & saved.mode
		}
		bits = 0
		for mode := multiKeyShare; mode <= multiUpdateAction; mode++ {
			required := keyShare
			switch mode {
			case multiShare:
				required = rowShare
			case multiNoKeyLock, multiNoKeyAction:
				required = noKeyUpdate
			case multiUpdateLock, multiUpdateAction:
				required = rowUpdate
			}
			if saved.multi[tuple.Version]&(1<<mode) != 0 && granted&required != 0 {
				bits |= 1 << mode
			}
		}
		if bits == 0 {
			delete(t.multiContributions, tuple)
		} else {
			t.multiContributions[tuple] = bits
		}
		row := m.multiRows[tuple]
		if row.id != 0 {
			m.retireMultiTupleLocked(tuple)
			continue
		}
		members := make([]multiMember, 0, len(row.members))
		for _, member := range row.members {
			if member.owner != t {
				members = append(members, member)
			}
		}
		for mode := multiKeyShare; mode <= multiUpdateAction; mode++ {
			if bits&(1<<mode) != 0 {
				members = append(members, multiMember{t.AssignedPublicXID(), mode, t})
			}
		}
		canonicalMultiMembers(members)
		row.members = members
		if len(members) == 0 {
			delete(m.multiRows, tuple)
		} else {
			m.multiRows[tuple] = row
		}
	}
}

func (m *relationLockManager) retireMultiOwnerLocked(owner *relationTicket) {
	for tuple := range owner.multiContributions {
		m.retireMultiTupleLocked(tuple)
	}
}

func (m *relationLockManager) retireMultiTupleLocked(tuple multiTuple) {
	for _, member := range m.multiRows[tuple].members {
		ticket := member.owner
		if ticket != nil && !ticket.OwnershipEnded() && ticket.CompletionState() != ids.TxAborted && ticket.multiContributions[tuple]&(1<<member.Mode) != 0 {
			return
		}
	}
	delete(m.multiRows, tuple)
}

func multiTransactionAge(next, xid uint32) int32 {
	if xid == 0 {
		return 2147483647
	}
	return int32(next - xid)
}
