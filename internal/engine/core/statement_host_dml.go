package core

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// statementHost implements dml.Host. The Statement methods read the scope at
// call time and never lock; the windows and waits are in
// statement_host_stage.go and statement_host_latest_row.go.
var _ dml.Host = (*statementHost)(nil)

// Command is the active slot: activateMutation and the rule and SQL routine
// save/restore copy whole mutations into it.
func (h *statementHost) Command() *dml.Command { return &h.StatementMutation.Command }

func (h *statementHost) Target() (ids.ObjectRef, *storage.Relation) {
	return h.LockedRef, h.LockedStorage
}

func (h *statementHost) PrivateRows(a ids.ObjectAddress) dml.PrivateRows {
	return dml.PrivateRows{Own: h.OwnWrites[a], OwnDeletes: h.OwnDeletes[a], Staged: h.Stage.Rows[a], StagedDeletes: h.Stage.Deletes[a]}
}

func (h *statementHost) Snapshot() (bool, ids.CommitSeq) {
	return h.Owner.Isolation.Stable(), h.Owner.Frontier
}

func (h *statementHost) UniqueJournal() lock.UniqueJournal { return h.scope().uniqueJournal() }
func (h *statementHost) QueryAuthority() *exec.Authority   { return h.authority }

func (h *statementHost) ForeignActions(allocate bool) *dml.ForeignActionScope {
	if allocate {
		return h.scope().foreignActions()
	}
	return h.foreignActionsSlot
}

func (h *statementHost) IndexScope(allocate bool) *index.Scope {
	if allocate {
		return h.scope().indexes()
	}
	return h.indexesSlot
}

func (h *statementHost) Ticket() *lock.Ticket { return h.LockTicket }

func (h *statementHost) CatalogRead(state *catalog.State) catalog.Read {
	return h.scope().Engine().Catalog.Read(state)
}

func (h *statementHost) PositionedRow(b analyze.Binding) (dml.MutationRowIdentity, error) {
	return h.scope().positionedMutationRow(b)
}

// CheckWriteBinding is the ON CONFLICT binding check of b over v with the
// operation's environment. It never locks.
func (h *statementHost) CheckWriteBinding(v access.View, b analyze.Binding) error {
	return analyze.CheckBinding(h.scope().Engine().operationAuthority(h.Op, v), b, &parser.Insert{})
}

// DisclosureEnv is the access environment of the statement's operation over
// v, for key disclosure decisions. It never locks.
func (h *statementHost) DisclosureEnv(v access.View) access.Environment {
	return h.scope().Engine().operationAuthority(h.Op, v)
}

func (h *statementHost) CommandError(err error) error     { return h.Op.CommandError(err) }
func (h *statementHost) CopyCommandError(err error) error { return h.scope().copyCommandError(err) }
func (h *statementHost) Operation() *session.Operation    { return h.Op }

func (h *statementHost) AcquireRow(c visibility.DeleteCandidate) error {
	return h.scope().acquireRow(c)
}

func (h *statementHost) AcquireUpdateRow(ref ids.ObjectRef, row ids.RowID, mode lock.Mode) error {
	return h.scope().acquireUpdateRowMode(ref, row, mode)
}

func (h *statementHost) AcquirePartitionLock(ref ids.ObjectRef, mode lock.Mode) error {
	return h.scope().AcquirePartitionLock(ref, mode)
}

func (h *statementHost) PhysicalRowEvent(ref ids.ObjectRef, row storage.RowVersion, mode lock.MultiMode, successor ids.VersionID) error {
	return h.scope().physicalRowEvent(ref, row, mode, successor)
}

// RowDetail is rowConstraintDetail for a constraint and rowDiagnosticDetail
// with empty suppression for a view check.
func (h *statementHost) RowDetail(b *analyze.Binding, row []sqltype.Value, p dml.DetailPurpose) (string, error) {
	s := h.scope()
	if p == dml.DetailViewCheck {
		return s.Engine().rowDiagnosticDetail(s, b, row, true)
	}
	return s.Engine().rowConstraintDetail(s, b, row)
}

// SchemaName takes Engine.mu for one private catalog read.
func (h *statementHost) SchemaName(ns ids.ObjectAddress) (string, bool) {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if d := e.PrivateCatalogLocked(s.PendingCatalog()).Get(ns); d != nil {
		return d.Name(), true
	}
	return "", false
}

func (h *statementHost) CaptureRegclassOutput(rows *exec.FinalRows) error {
	s := h.scope()
	return s.Engine().captureRegclassOutput(s.Op, rows)
}

// uniqueJournal is the statement ticket's unique-key journal.
func (s *StatementScope) uniqueJournal() lock.UniqueJournal {
	return s.Engine().Locks.UniqueJournal(s.LockTicket)
}
