package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// statementHost implements dml.Host. The Statement methods read the scope at
// call time and never lock; the windows and waits are in
// statement_host_stage.go and statement_host_latest_row.go.
var _ dml.Host = (*statementHost)(nil)

// Command is the active slot: activateMutation and the rule and SQL routine
// save/restore copy whole mutations into it.
func (h *statementHost) Command() *dml.Command { return &h.statementMutation.Command }

func (h *statementHost) Target() (ids.ObjectRef, *storage.Relation) {
	return h.lockedRef, h.lockedStorage
}

func (h *statementHost) PrivateRows(a ids.ObjectAddress) dml.PrivateRows {
	return dml.PrivateRows{Own: h.ownWrites[a], OwnDeletes: h.ownDeletes[a], Staged: h.stage.rows[a], StagedDeletes: h.stage.deletes[a]}
}

func (h *statementHost) Snapshot() (bool, ids.CommitSeq) {
	return stableIsolation(h.owner.Isolation), h.owner.Frontier
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

func (h *statementHost) Ticket() *lock.Ticket { return h.ticket }

func (h *statementHost) CatalogRead(state *catalog.State) catalog.Read {
	return h.scope().engine().catalog.read(state)
}

func (h *statementHost) PositionedRow(b analyze.Binding) (dml.MutationRowIdentity, error) {
	return h.scope().positionedMutationRow(b)
}

// CheckWriteBinding checks b over v with the operation's environment.
func (h *statementHost) CheckWriteBinding(v access.View, b analyze.Binding) error {
	return h.scope().checkWriteBinding(v, b)
}

func (h *statementHost) DisclosureEnv(v access.View) access.Environment {
	return h.scope().disclosureEnv(v)
}

func (h *statementHost) CommandError(err error) error     { return h.op.CommandError(err) }
func (h *statementHost) CopyCommandError(err error) error { return h.scope().copyCommandError(err) }

func (h *statementHost) AcquireRow(c visibility.DeleteCandidate) error {
	return h.scope().acquireRow(c)
}

func (h *statementHost) AcquireUpdateRow(ref ids.ObjectRef, row ids.RowID, mode lock.Mode) error {
	return h.scope().acquireUpdateRowMode(ref, row, mode)
}

func (h *statementHost) AcquirePartitionLock(ref ids.ObjectRef, mode lock.Mode) error {
	return h.scope().acquirePartitionLock(ref, mode)
}

func (h *statementHost) PhysicalRowEvent(ref ids.ObjectRef, row storage.RowVersion, mode lock.MultiMode, successor ids.VersionID) error {
	return h.scope().physicalRowEvent(ref, row, mode, successor)
}

// RowDetail is rowConstraintDetail for a constraint and rowDiagnosticDetail
// with empty suppression for a view check.
func (h *statementHost) RowDetail(b *analyze.Binding, row []sqltype.Value, p dml.DetailPurpose) (string, error) {
	s := h.scope()
	if p == dml.DetailViewCheck {
		return s.engine().rowDiagnosticDetail(s, b, row, true)
	}
	return s.engine().rowConstraintDetail(s, b, row)
}

// SchemaName takes Engine.mu for one private catalog read.
func (h *statementHost) SchemaName(ns ids.ObjectAddress) (string, bool) {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if d := e.privateCatalogLocked(s.owner.Catalog).Get(ns); d != nil {
		return d.Name(), true
	}
	return "", false
}

func (h *statementHost) CaptureRegclassOutput(rows *exec.FinalRows) error {
	s := h.scope()
	return s.engine().captureRegclassOutput(s.op, rows)
}
