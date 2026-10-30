package session

import (
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// Owner transitions edit only the owner's own fields (and, where named, the
// settings store's characteristic projections). The root runs them under t.Mu
// after its own cursor, portal, extension and exclusion work, and keeps every
// SSI, lock-manager and timeout call.

// BeginLocked starts an incarnation whose Ref the root already minted: state,
// the default characteristics, entry modes and start time. settings.Mu is
// held.
func (t *TransactionOwner) BeginLocked(settings *SettingsStore, state TxState, start time.Time) {
	t.State = state
	t.Isolation = IsolationFromText(settings.Value("default_transaction_isolation"))
	t.ReadOnly = settings.Value("default_transaction_read_only") == "on"
	t.Deferrable = settings.Value("default_transaction_deferrable") == "on"
	t.EntryIsolation, t.EntryReadOnly = t.Isolation, t.ReadOnly
	t.EntryDeferrable = t.Deferrable
	settings.CurrentIsolation = IsolationText(t.Isolation)
	settings.CurrentReadOnly = t.ReadOnly
	settings.CurrentDeferrable = t.Deferrable
	t.Start = start
	if t.RetainedMessage {
		t.MessageBorn = true
	}
}

// Clear returns the owner to idle. It keeps Next, the ticket, the
// characteristics and the savepoint serial.
func (t *TransactionOwner) Clear() {
	if t.ProtocolCycle != nil {
		t.ProtocolCycle.Executed = false
	}
	t.State = TxIdle
	t.Ref = ids.TxRef{}
	t.TempTablespaces = TempTablespaceSelector{}
	t.Command = 0
	t.Writes = nil
	t.Deletes = nil
	t.Start = time.Time{}
	t.SnapshotSet = false
	t.CurrentSnapshot = nil
	t.Frontier = 0
	t.Roots = nil
	t.SequenceCatalog = nil
	t.Catalog = nil
	t.NotificationActions = nil
	t.NotificationEvents = nil
	clear(t.Savepoints)
	t.Savepoints = nil
	t.AccessedTemporary = false
}

// Abort marks an explicit block without savepoints failed: it restores the
// entry modes, drops the private effects and detaches the ticket, which it
// returns. The block keeps its Ref until ROLLBACK.
func (t *TransactionOwner) Abort() *lock.Ticket {
	t.Isolation, t.ReadOnly = t.EntryIsolation, t.EntryReadOnly
	t.Deferrable = t.EntryDeferrable
	t.State = TxAborted
	t.TempTablespaces = TempTablespaceSelector{}
	t.Writes = nil
	t.Deletes = nil
	t.Roots = nil
	t.SequenceCatalog = nil
	t.CurrentSnapshot = nil
	t.Frontier = 0
	t.Catalog = nil
	t.NotificationActions = nil
	t.NotificationEvents = nil
	ticket := t.Ticket
	t.Ticket = nil
	return ticket
}

// ChainModes are the characteristics AND CHAIN carries into the next block.
// Abort pops every retained child before PostgreSQL selects them, so a
// rollback or a failed block takes the outermost access mode; a healthy commit
// carries the current child mode.
func (t *TransactionOwner) ChainModes(commit bool) TxOptions {
	modes := TxOptions{Isolation: t.Isolation, ReadOnly: t.ReadOnly, Deferrable: t.Deferrable}
	if (!commit || t.State == TxAborted) && len(t.Savepoints) != 0 {
		modes.ReadOnly = t.Savepoints[0].ReadOnly
	}
	return modes
}

// InstallChain installs the next block of AND CHAIN on a cleared owner. The
// modes are its unstacked baseline, unlike BEGIN's overrides of the session
// defaults, so they are also the entry modes.
func (t *TransactionOwner) InstallChain(next ids.TxRef, modes TxOptions, start time.Time, ticket *lock.Ticket) {
	t.Ref = next
	t.State = TxActive
	t.Isolation, t.ReadOnly = modes.Isolation, modes.ReadOnly
	t.Deferrable = modes.Deferrable
	t.EntryIsolation, t.EntryReadOnly = modes.Isolation, modes.ReadOnly
	t.EntryDeferrable = modes.Deferrable
	t.Start = start
	t.Ticket = ticket
}

// PrepareRefusal reports why PREPARE TRANSACTION gid cannot detach this block,
// in PostgreSQL's order. The root passes whether the block created a current
// WITH HOLD cursor and whether it has foreign participants.
func (t *TransactionOwner) PrepareRefusal(gid string, withHold, foreign bool) error {
	switch {
	case withHold:
		return sqlError("0A000", "cannot PREPARE a transaction that has created a cursor WITH HOLD")
	case len(gid) >= 200:
		return sqlError("22023", `transaction identifier "`+gid+`" is too long`)
	case t.AccessedTemporary:
		return sqlError("0A000", "cannot PREPARE a transaction that has operated on temporary objects")
	case foreign:
		return sqlError("0A000", "cannot PREPARE a transaction that has operated on foreign tables")
	case len(t.NotificationActions) != 0 || len(t.NotificationEvents) != 0:
		return sqlError("0A000", "cannot PREPARE a transaction that has executed LISTEN, UNLISTEN, or NOTIFY")
	}
	return nil
}

// PreparedEntry is the registry entry PREPARE TRANSACTION gid would install
// for the statement's incarnation ref, sharing the block's roots, journal and
// ticket. The publication preflight rejects it if ref is no longer t.Ref.
func (t *TransactionOwner) PreparedEntry(gid string, ref ids.TxRef, owner authorization.PrincipalRef, database string, at time.Time) *PreparedTransaction {
	return &PreparedTransaction{GID: gid, Owner: owner, Ref: ref, Prepared: at, Database: database, Ticket: t.Ticket, Writes: t.Writes, Deletes: t.Deletes, Catalog: t.Catalog, Roots: t.Roots, Frontier: t.Frontier, Options: TxOptions{Isolation: t.Isolation, ReadOnly: t.ReadOnly, Deferrable: t.Deferrable}}
}
