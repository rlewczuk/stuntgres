package core

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/logical"
	"stuntgres/internal/engine/logical/testdecoding"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqlstate"
)

// Startup is an engine's immutable startup configuration. LogicalWAL starts
// the cluster with wal_level=logical; OutputPlugins are the logical decoding
// output plugins slots may name.
type Startup struct {
	TrackCommitTimestamp bool
	LogicalWAL           bool
	OutputPlugins        map[string]logical.Factory
}

// logicalCluster is the cluster's logical stream: the startup capability, the
// stream position allocator and the cluster-wide slot registry. Cluster.Mu
// guards it. Plugins never run under that lock.
type logicalCluster struct {
	wal     bool
	plugins map[string]logical.Factory // Immutable after startup.
	lsn     logical.Allocator
	slots   map[string]*logicalSlot
	serial  uint64 // Creation order, PostgreSQL's slot array order.
}

func (c *logicalCluster) configure(startup Startup) error {
	c.wal = startup.LogicalWAL
	for name, factory := range startup.OutputPlugins {
		if name == "" || factory == nil {
			return fmt.Errorf("stuntgres: invalid logical output plugin %q", name)
		}
	}
	// Explicit startup providers take precedence, including an intentional
	// replacement of a stock name. Restore retains this exact composition.
	if c.wal || len(startup.OutputPlugins) != 0 {
		c.plugins = map[string]logical.Factory{"test_decoding": testdecoding.New}
		maps.Copy(c.plugins, startup.OutputPlugins)
	}
	return nil
}

// logicalSlot is one logical replication slot. Every field is guarded by
// Cluster.Mu. A slot is held by at most one session: a temporary slot by its
// creator for its whole life, a persistent slot during one operation.
type logicalSlot struct {
	name          string
	target        *Engine // The slot's database.
	plugin        string
	temporary     bool
	twoPhase      bool
	twoPhaseAt    uint64
	failover      bool
	creating      bool // No consistent point yet; confirmed is zero.
	restart       uint64
	confirmed     uint64
	catalogXmin   uint32
	holder        ids.SessionToken
	held          bool
	inactiveSince time.Time
	statsReset    time.Time
	serial        uint64
}

func (s *logicalSlot) activePID() int32 {
	if !s.held {
		return 0
	}
	return int32(s.holder.ID())
}

func slotMissing(name string) error {
	return SQLError("42704", fmt.Sprintf(`replication slot "%s" does not exist`, name))
}

func slotActive(s *logicalSlot) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55006", Message: fmt.Sprintf(`replication slot "%s" is active for PID %d`, s.name, s.activePID())}
}

func logicalWALRequired() error {
	return SQLError("55000", `logical decoding requires "wal_level" >= "logical"`)
}

// acquireSlotLocked makes token the slot's holder, as ReplicationSlotAcquire
// with nowait: a slot held by another session fails with 55006.
// The live cluster slot entry, not target admission, owns acquisition. PostgreSQL
// permits a peer database to acquire an existing slot during database removal;
// the remover must recheck held slots before its atomic retirement boundary.
func (e *Engine) acquireSlotLocked(name string, token ids.SessionToken) (*logicalSlot, error) {
	slot := e.Cluster.logical.slots[name]
	if slot == nil {
		return nil, slotMissing(name)
	}
	if slot.held && slot.holder != token {
		return nil, slotActive(slot)
	}
	slot.holder, slot.held = token, true
	slot.inactiveSince = time.Time{}
	return slot, nil
}

// releaseSlotLocked ends one operation's use. Temporary slots stay held by
// their creating session.
func (e *Engine) releaseSlotLocked(slot *logicalSlot) {
	if slot.temporary || e.Cluster.logical.slots[slot.name] != slot {
		return
	}
	slot.held, slot.holder = false, ids.SessionToken{}
	slot.inactiveSince = time.Now()
}

// registerSlotLocked publishes a slot and starts capture for its database.
func (e *Engine) registerSlotLocked(slot *logicalSlot) {
	c := &e.Cluster.logical
	if c.slots == nil {
		c.slots = make(map[string]*logicalSlot)
	}
	c.serial++
	slot.serial = c.serial
	c.slots[slot.name] = slot
}

// logicalCaptureLocked reports whether changes are captured: while any slot of
// the cluster exists, as WAL is cluster-wide, so a slot copied into another
// database finds that database's history from its source's position.
func (e *Engine) logicalCaptureLocked() bool {
	return e.Cluster != nil && len(e.Cluster.logical.slots) != 0
}

// dropSlotLocked removes a slot and releases history no remaining slot of its
// database needs.
func (e *Engine) dropSlotLocked(slot *logicalSlot) {
	c := &e.Cluster.logical
	if c.slots[slot.name] != slot {
		return
	}
	delete(c.slots, slot.name)
	slot.held = false
	e.trimLogicalHistoryLocked()
}

// trimLogicalHistoryLocked keeps, in every database, the records some slot of
// the cluster may still need, including open and prepared transaction anchors.
func (e *Engine) trimLogicalHistoryLocked() {
	keep := ^uint64(0)
	for _, slot := range e.Cluster.logical.slots {
		slot.restart = logical.RestartLSN(slot.target.logicalHistory.Snapshot(), slot.confirmed)
		keep = min(keep, slot.restart)
	}
	for _, target := range e.Cluster.Targets {
		if len(e.Cluster.logical.slots) == 0 {
			target.logicalHistory.Reset()
		} else {
			target.logicalHistory.Trim(logical.RestartLSN(target.logicalHistory.Snapshot(), keep))
		}
	}
}

// releaseSessionSlotsLocked drops token's temporary slots and releases any
// slot it still holds, as ReplicationSlotRelease and ReplicationSlotCleanup do
// at top-level error recovery and at backend exit.
func (e *Engine) releaseSessionSlotsLocked(token ids.SessionToken) {
	if e.Cluster == nil {
		return
	}
	for _, slot := range e.Cluster.logical.slots {
		if !slot.held || slot.holder != token {
			continue
		}
		if slot.temporary || slot.creating {
			e.dropSlotLocked(slot)
		} else {
			e.releaseSlotLocked(slot)
		}
	}
}

// checkReplacementSlotsLocked fails with 55006 when a slot of e that catalog
// replacement would drop is in use. A database being created has no slots.
func (e *Engine) checkReplacementSlotsLocked() error {
	if e.Cluster == nil {
		return nil
	}
	for _, slot := range e.Cluster.logical.slots {
		if slot.target == e && slot.held {
			return slotActive(slot)
		}
	}
	return nil
}

// dropReplacedSlotsLocked removes e's slots and history at catalog
// replacement: the replacement's history is unrelated to the old stream.
func (e *Engine) dropReplacedSlotsLocked() {
	if e.Cluster != nil {
		for _, slot := range e.Cluster.logical.slots {
			if slot.target == e {
				e.dropSlotLocked(slot)
			}
		}
	}
	e.logicalHistory.Reset()
}

// LogicalSlotCountLocked counts the logical slots of database e, and how many
// of them are active, for database lifecycle checks.
func (e *Engine) LogicalSlotCountLocked() (slots, active int) {
	for _, slot := range e.Cluster.logical.slots {
		if slot.target == e {
			slots++
			if slot.held {
				active++
			}
		}
	}
	return slots, active
}

// replicationSlotsLocked snapshots every slot of the cluster in creation order.
func (e *Engine) replicationSlotsLocked() []pgcatalog.ReplicationSlot {
	if e.Cluster == nil || len(e.Cluster.logical.slots) == 0 {
		return nil
	}
	slots := slices.SortedFunc(maps.Values(e.Cluster.logical.slots), func(a, b *logicalSlot) int { return int(a.serial) - int(b.serial) })
	rows := make([]pgcatalog.ReplicationSlot, len(slots))
	for i, slot := range slots {
		rows[i] = pgcatalog.ReplicationSlot{Name: slot.name, Plugin: slot.plugin, Database: slot.target.DatabaseName, DatabaseOID: slot.target.Catalog.DatabaseAddress().ObjectID, Temporary: slot.temporary, ActivePID: slot.activePID(), CatalogXmin: slot.catalogXmin, Restart: slot.restart, Confirmed: slot.confirmed, TwoPhase: slot.twoPhase, TwoPhaseAt: slot.twoPhaseAt, InactiveSince: slot.inactiveSince, Failover: slot.failover, StatsReset: slot.statsReset}
	}
	return rows
}

// releaseSlotsAfterError is PostgreSQL's top-level error recovery: the
// session's temporary slots are dropped and any slot it holds is released.
func (s *StatementScope) releaseSlotsAfterError() {
	e := s.Engine()
	if e == nil || e.Cluster == nil || s.Op == nil || s.Op.Session == nil {
		return
	}
	e.Mu.Lock()
	e.releaseSessionSlotsLocked(s.Op.Session.Token())
	e.Mu.Unlock()
}
