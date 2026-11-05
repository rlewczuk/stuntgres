package core

import (
	"errors"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/logical"
	"stuntgres/internal/engine/replication"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Lock order: Engine.Mu (the cluster mutex) guards slots and history. Plugin
// callbacks and transaction waits run with no engine lock held; every path that
// holds a slot releases it under Engine.Mu on exit.

// replicationRole borrows current authority under Engine -> Session ordering
// and requires a superuser or the actor's own REPLICATION attribute.
func (h *statementHost) replicationRole() error {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	if !actor.Superuser && !actor.Replication {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied to use replication slots", Detail: "Only roles with the REPLICATION attribute may use replication slots."}
	}
	return nil
}

func (h *statementHost) ReplicationSlot(kind functions.ReplicationSlotKind, arity uint8, values [5]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := h.replicationRole(); err != nil {
		return sqltype.Value{}, err
	}
	name, err := values[0].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	switch kind {
	case functions.SlotCreatePhysical:
		if err := replication.ValidateSlotName(name); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Value{}, SQLError("0A000", "pg_create_physical_replication_slot physical replication slots and WAL reservation are unsupported in embedded mode")
	case functions.SlotCreateLogical:
		return h.createLogicalSlot(name, values, result)
	case functions.SlotCopyLogical:
		return h.copyLogicalSlot(name, arity, values, result)
	case functions.SlotCopyPhysical:
		return sqltype.Value{}, h.copyPhysicalSlot(name)
	case functions.SlotAdvance:
		return h.advanceSlot(name, values[1], result)
	case functions.SlotDrop:
		if err := h.dropSlot(name); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.VoidValue(result), nil
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid replication slot operation %d/%d", kind, arity)
}

func slotResult(result *sqltype.Descriptor, name string, lsn uint64) (sqltype.Value, error) {
	if result == nil || result.CompositeLen() != 2 {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid replication slot result")
	}
	position := sqltype.Null(result.CompositeMember(1).Usage.Type)
	if lsn != 0 {
		var err error
		if position, err = sqltype.PGLSNValue(result.CompositeMember(1).Usage.Type, lsn); err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.OwnedCompositeValue(result, []sqltype.Value{sqltype.String(result.CompositeMember(0).Usage.Type, name), position})
}

func optionalBool(v sqltype.Value, fallback bool) (bool, error) {
	if v.Type() == nil {
		return fallback, nil
	}
	return v.Boolean()
}

// slotRequest is a logical slot to create: a new slot or a copy's target.
type slotRequest struct {
	name, plugin                  string
	temporary, twoPhase, failover bool
	source                        *logicalSlot // Copy source; nil for creation.
}

func (h *statementHost) createLogicalSlot(name string, values [5]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	if !h.scope().Engine().Cluster.logical.wal {
		return sqltype.Value{}, logicalWALRequired()
	}
	plugin, err := values[1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	req := slotRequest{name: name, plugin: plugin}
	if req.temporary, err = optionalBool(values[2], false); err != nil {
		return sqltype.Value{}, err
	}
	if req.twoPhase, err = optionalBool(values[3], false); err != nil {
		return sqltype.Value{}, err
	}
	if req.failover, err = optionalBool(values[4], false); err != nil {
		return sqltype.Value{}, err
	}
	confirmed, err := h.newLogicalSlot(req)
	if err != nil {
		return sqltype.Value{}, err
	}
	return slotResult(result, req.name, confirmed)
}

func (h *statementHost) copyLogicalSlot(source string, arity uint8, values [5]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	e := s.Engine()
	if !e.Cluster.logical.wal {
		return sqltype.Value{}, logicalWALRequired()
	}
	target, err := values[1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	e.Mu.Lock()
	src := e.Cluster.logical.slots[source]
	var req slotRequest
	if src != nil {
		req = slotRequest{name: target, plugin: src.plugin, temporary: src.temporary, source: src}
	}
	e.Mu.Unlock()
	if src == nil {
		return sqltype.Value{}, slotMissing(source)
	}
	if arity >= 3 {
		if req.temporary, err = values[2].Boolean(); err != nil {
			return sqltype.Value{}, err
		}
	}
	if arity >= 4 {
		if req.plugin, err = values[3].Text(); err != nil {
			return sqltype.Value{}, err
		}
	}
	confirmed, err := h.newLogicalSlot(req)
	if err != nil {
		return sqltype.Value{}, err
	}
	return slotResult(result, req.name, confirmed)
}

// copyPhysicalSlot reaches only absent or logical sources: no physical slot
// can exist natively.
func (h *statementHost) copyPhysicalSlot(source string) error {
	e := h.scope().Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if e.Cluster.logical.slots[source] == nil {
		return slotMissing(source)
	}
	return SQLError("0A000", fmt.Sprintf(`cannot copy logical replication slot "%s" as a physical replication slot`, source))
}

// newLogicalSlot follows ReplicationSlotCreate and CreateInitDecodingContext:
// the target is published as in-progress (capture starts, the name is taken),
// the plugin is admitted and started, and a new slot then waits for every
// transaction holding an XID before taking its consistent point. A copy takes
// its source's positions instead. Failure removes the in-progress target. It
// returns the confirmed position read under the lock that publishes the slot.
func (h *statementHost) newLogicalSlot(req slotRequest) (uint64, error) {
	s := h.scope()
	e := s.Engine()
	if err := replication.ValidateSlotName(req.name); err != nil {
		return 0, err
	}
	if req.temporary && req.failover {
		return 0, SQLError("0A000", "cannot enable failover for a temporary replication slot")
	}
	token := s.Op.Session.Token()
	capacity := -1
	if text, ok := s.Op.Setting("max_replication_slots"); ok {
		if n, err := strconv.Atoi(text); err == nil {
			capacity = n
		}
	}
	slot := &logicalSlot{name: req.name, target: e, plugin: req.plugin, temporary: req.temporary, twoPhase: req.twoPhase, failover: req.failover, creating: true, holder: token, held: true}
	e.Mu.Lock()
	err := h.admitSlotLocked(slot, capacity)
	if err == nil {
		e.registerSlotLocked(slot)
		slot.restart = e.Cluster.logical.lsn.End()
		slot.catalogXmin = uint32(e.Visibility.LastAssignedXID() + 1)
	}
	e.Mu.Unlock()
	if err != nil {
		return 0, err
	}
	published := false
	defer func() {
		if !published {
			e.Mu.Lock()
			e.dropSlotLocked(slot)
			e.Mu.Unlock()
		}
	}()
	plugin, _, err := h.startPlugin(slot, nil, true)
	if err != nil {
		return 0, err
	}
	plugin.Shutdown()
	if req.source == nil {
		if err := h.awaitRunningTransactions(); err != nil {
			return 0, err
		}
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := s.Op.Context().Err(); err != nil {
		return 0, err
	}
	if src := req.source; src != nil {
		if e.Cluster.logical.slots[src.name] != src {
			return 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: fmt.Sprintf(`could not copy replication slot "%s"`, src.name), Detail: "The source replication slot was modified incompatibly during the copy operation."}
		}
		if src.creating {
			return 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`cannot copy unfinished logical replication slot "%s"`, src.name), Hint: "Retry when the source replication slot's confirmed_flush_lsn is valid."}
		}
		slot.restart, slot.confirmed, slot.catalogXmin = src.restart, src.confirmed, src.catalogXmin
	} else {
		slot.confirmed = e.Cluster.logical.lsn.End()
		slot.restart = slot.confirmed
		if slot.twoPhase {
			slot.twoPhaseAt = slot.confirmed
		}
	}
	slot.creating = false
	e.releaseSlotLocked(slot)
	published = true
	return slot.confirmed, nil
}

// admitSlotLocked applies the name, capacity and transaction checks of slot
// creation in PostgreSQL's order.
func (h *statementHost) admitSlotLocked(slot *logicalSlot, capacity int) error {
	s := h.scope()
	e := s.Engine()
	c := &e.Cluster.logical
	if c.slots[slot.name] != nil {
		return SQLError("42710", fmt.Sprintf(`replication slot "%s" already exists`, slot.name))
	}
	if capacity >= 0 && len(c.slots) >= capacity {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "53400", Message: "all replication slots are in use", Hint: `Free one or increase "max_replication_slots".`}
	}
	if e.Visibility.Entries[s.TxRef].AssignedXID != 0 {
		return SQLError("25001", "cannot create logical replication slot in transaction that has performed writes")
	}
	return nil
}

// startPlugin admits the slot's plugin against output_plugin_libraries, loads
// it and runs its startup callback, outside Engine.Mu.
func (h *statementHost) startPlugin(slot *logicalSlot, options []logical.Option, init bool) (logical.Plugin, logical.Options, error) {
	s := h.scope()
	list, _ := s.Op.Setting("output_plugin_libraries")
	if !replication.PluginAllowed(list, slot.plugin) {
		return nil, logical.Options{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`library "%s" may not be used as an output plugin`, slot.plugin), Hint: `If it is safe for all REPLICATION users to use this library as an output plugin, add it to "output_plugin_libraries" and reload the server configuration.`}
	}
	factory := s.Engine().Cluster.logical.plugins[slot.plugin]
	if factory == nil {
		return nil, logical.Options{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "58P01", Message: fmt.Sprintf(`could not access file "%s": No such file or directory`, slot.plugin)}
	}
	plugin := factory()
	opts, err := plugin.Startup(logical.Startup{Slot: slot.name, Options: options, Init: init, TwoPhase: slot.twoPhase, Output: s.Output})
	if err != nil {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) {
			copy := *diagnostic
			copy.Context = fmt.Sprintf("slot %q, output plugin %q, in the startup callback", slot.name, slot.plugin)
			err = sqlstate.WithDiagnostic(err, &copy)
		}
		plugin.Shutdown()
		return nil, logical.Options{}, err
	}
	return plugin, opts, nil
}

// awaitRunningTransactions waits, cancellably, for every transaction of the
// cluster that holds an XID, including prepared ones: none of them can be
// partially visible to the new slot.
func (h *statementHost) awaitRunningTransactions() error {
	s := h.scope()
	e := s.Engine()
	var tickets []*lock.Ticket
	e.Mu.Lock()
	for _, target := range e.Cluster.Targets {
		for ref, entry := range target.Visibility.Entries {
			if ref == s.TxRef || entry.FullXID == 0 {
				continue
			}
			if record, ok := target.Visibility.History[entry.FullXID]; ok && record.State == ids.TxInProgress && record.Ticket != nil {
				tickets = append(tickets, record.Ticket)
			}
		}
	}
	e.Mu.Unlock()
	for _, ticket := range tickets {
		if err := h.WaitTransaction(ticket, lock.CompletionTransaction); err != nil {
			return err
		}
	}
	return nil
}

func (h *statementHost) advanceSlot(name string, target sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	moveto, err := target.PGLSN()
	if err != nil {
		return sqltype.Value{}, err
	}
	if moveto == 0 {
		return sqltype.Value{}, SQLError("22023", "invalid target WAL LSN")
	}
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	moveto = min(moveto, e.Cluster.logical.lsn.End())
	slot, err := e.acquireSlotLocked(name, s.Op.Session.Token())
	if err != nil {
		return sqltype.Value{}, err
	}
	defer e.releaseSlotLocked(slot)
	if moveto < slot.confirmed {
		return sqltype.Value{}, SQLError("55000", fmt.Sprintf("cannot advance replication slot to %s, minimum is %s", logical.FormatLSN(moveto), logical.FormatLSN(slot.confirmed)))
	}
	e.confirmSlotLocked(slot, moveto)
	return slotResult(result, slot.name, slot.confirmed)
}

// confirmSlotLocked moves the slot forward to lsn, never backward, and
// releases history no slot of the database still needs.
func (e *Engine) confirmSlotLocked(slot *logicalSlot, lsn uint64) {
	if lsn <= slot.confirmed {
		return
	}
	slot.confirmed = lsn
	slot.restart = logical.RestartLSN(slot.target.logicalHistory.Snapshot(), lsn)
	e.trimLogicalHistoryLocked()
}

func (h *statementHost) dropSlot(name string) error {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	slot, err := e.acquireSlotLocked(name, s.Op.Session.Token())
	if err != nil {
		return err
	}
	e.dropSlotLocked(slot)
	return nil
}
