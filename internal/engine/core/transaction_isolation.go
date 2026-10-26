package core

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func syncCharacteristics(t *session.TransactionOwner) {
	settings := t.Session.Settings
	settings.Mu.Lock()
	settings.CurrentIsolation = session.IsolationText(t.Isolation)
	settings.CurrentReadOnly = t.ReadOnly
	settings.CurrentDeferrable = t.Deferrable
	settings.Mu.Unlock()
	e := EngineOf(t.Session)
	e.Mu.Lock()
	if !t.SnapshotSet && (t.State == session.TxActive || t.State == session.TxImplicit) {
		e.enrollSerializableLocked(t.Ref, t.Isolation)
	}
	e.Mu.Unlock()
}
func assignModes(t *session.TransactionOwner, modes []parser.TransactionMode) error {
	if err := t.AssignModes(modes); err != nil {
		return err
	}
	syncCharacteristics(t)
	return nil
}

func (e *Engine) applyCurrentCharacteristic(op *session.Operation, d settings.Definition, input *string) (string, error) {
	if input == nil {
		return "", SQLError("0A000", fmt.Sprintf(`parameter "%s" cannot be reset`, d.Name))
	}
	value, err := d.Parse(*input)
	if err != nil {
		return "", err
	}
	t := op.Session.Transactions
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if err := e.endPreflight(op); err != nil {
		return "", err
	}
	mode := session.CharacteristicMode(d.Name, d.Format(value))
	if err := assignModes(t, []parser.TransactionMode{mode}); err != nil {
		return "", err
	}
	return t.Characteristic(d.Name), nil
}
func (s *StatementScope) executeCharacteristics(stmt *parser.TransactionCharacteristics, count int) (Result, error) {
	if count != 0 {
		return Result{}, errors.New("stuntgres: transaction characteristics take no arguments")
	}
	e, t := s.Engine(), s.Owner
	if !s.Op.CommandAdmission {
		s.Op.SnapshotNoticeThreshold()
	}
	if stmt.Session {
		for _, mode := range stmt.Modes {
			name, value := session.SessionCharacteristicSetting(mode)
			e.Mu.Lock()
			d := e.Settings.Definitions()[name]
			e.Mu.Unlock()
			if _, err := e.applySetting(s.Op, t.Session.Settings, d, &value, false); err != nil {
				return Result{}, err
			}
		}
		s.CompleteUtility()
		return Result{}, nil
	}
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if err := e.endPreflight(s.Op); err != nil {
		return Result{}, err
	}
	if !t.InBlock() {
		s.Op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET TRANSACTION can only be used in transaction blocks"})
	}
	err := assignModes(t, stmt.Modes)
	if err == nil {
		s.CompleteUtility()
	}
	return Result{}, err
}

// Analysis can establish a transaction-stable snapshot, but RC analysis must
// not select the later command's execution frontier.
func (s *StatementScope) snapshotAnalysisPhase(stmt parser.Statement) error {
	if !s.Owner.Isolation.Stable() {
		return nil
	}
	return s.snapshotPhase(stmt)
}

// Utilities enter with an active snapshot before their internal relation waits.
// Ordinary RC commands select theirs only after relation admission.
func (s *StatementScope) snapshotExecutionAdmission(stmt parser.Statement) error {
	switch stmt.(type) {
	case *parser.CreateQueryTable, *parser.RefreshMaterializedView:
		return s.snapshotPhase(stmt)
	default:
		return s.snapshotAnalysisPhase(stmt)
	}
}

func (s *StatementScope) snapshotPhase(stmt parser.Statement) error {
	if !session.SnapshotStatement(stmt) {
		return nil
	}
	previous := s.snapshotTargetAdmission
	s.snapshotTargetAdmission = session.SnapshotTargetAdmission(stmt)
	defer func() { s.snapshotTargetAdmission = previous }()
	return s.snapshotDataPhase()
}

// Fastpath has no SQL AST but obtains the same transaction snapshot before
// reading its function identity or invoking argument/result codecs.
func (s *StatementScope) snapshotDataPhase() error {
	t, e := s.Owner, s.Engine()
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if err := s.AcquireSerializableSnapshot(); err != nil {
		return err
	}
	if t.SnapshotSet && !t.NeedsSnapshotProjection(t.Roots) && s.currentSnapshot != nil {
		return nil
	}
	sess := s.Op.Session
	e.Mu.Lock()
	sess.Mu.Lock()
	check := func() error {
		if err := PublicationOperationLocked(e, s.Op); err != nil {
			return err
		}
		if t.Ref != s.TxRef || t.Ref.Epoch() != e.Catalog.State.Epoch() {
			return ids.ErrCatalogReplaced
		}
		return nil
	}
	if err := check(); err != nil {
		sess.Mu.Unlock()
		e.Mu.Unlock()
		return err
	}
	if !t.SnapshotSet && t.Isolation.Stable() {
		if _, err := e.VisibilitySnapshotLocked(t.Ref, false); err != nil {
			sess.Mu.Unlock()
			e.Mu.Unlock()
			return err
		}
		if err := e.retainTransactionSnapshotLocked(t); err != nil {
			sess.Mu.Unlock()
			e.Mu.Unlock()
			return err
		}
	}
	roots, frontier := t.Roots, t.Frontier
	sequenceCatalog := t.SequenceCatalog
	if !t.NeedsSnapshotProjection(roots) {
		t.Roots, t.Frontier = roots, frontier
		t.SequenceCatalog = sequenceCatalog
		t.SnapshotSet = true
		err := e.captureStatementSnapshotLocked(s)
		sess.Mu.Unlock()
		e.Mu.Unlock()
		return err
	}
	catalog := t.Catalog
	sess.Mu.Unlock()
	e.Mu.Unlock()
	// Raw roots remain the committed authority. Dense private projections are
	// prepared outside metadata locks and attached only after final admission.
	projected, err := session.ProjectPrivateSnapshotRoots(s.Op.Context(), catalog, roots)
	if err != nil {
		return err
	}
	e.Mu.Lock()
	sess.Mu.Lock()
	err = check()
	if err == nil {
		t.Catalog = projected
		t.Roots, t.Frontier = roots, frontier
		t.SequenceCatalog = sequenceCatalog
		t.SnapshotSet = true
		err = e.captureStatementSnapshotLocked(s)
	}
	sess.Mu.Unlock()
	e.Mu.Unlock()
	return err
}

// captureStatementSnapshotLocked installs the command's immutable public value
// and tuple frontier together. Engine.mu is held, and stable owner acquisition
// (including any SSI safe-snapshot wait) has already completed.
func (e *Engine) captureStatementSnapshotLocked(s *StatementScope) error {
	if s.currentSnapshot != nil {
		return nil
	}
	t := s.Owner
	if t.Isolation.Stable() {
		if !t.SnapshotSet || t.CurrentSnapshot == nil {
			return SQLError("55000", "no active transaction snapshot")
		}
		s.currentSnapshot, s.snapshotFrontier = t.CurrentSnapshot, t.Frontier
		return nil
	}
	snapshot, err := e.Visibility.CurrentSnapshotLocked(t.Ref)
	if err != nil {
		return err
	}
	s.currentSnapshot, s.snapshotFrontier = snapshot, e.Visibility.CommitSeq()
	s.retainSnapshotTargetRootsLocked(e)
	// RC retains the command pair and admitted target roots, not transaction-wide
	// roots. Snapshot acquisition closes the transaction-characteristics window.
	t.SnapshotSet = true
	return nil
}

// retainTransactionSnapshotLocked selects all stable owner state in one
// Engine.mu window. SSI uses the same acquisition for each safe candidate;
// SnapshotSet is installed only after that candidate/projection is accepted.
func (e *Engine) retainTransactionSnapshotLocked(t *session.TransactionOwner) error {
	snapshot, err := e.Visibility.CurrentSnapshotLocked(t.Ref)
	if err != nil {
		return err
	}
	t.CurrentSnapshot, t.Frontier = snapshot, e.Visibility.CommitSeq()
	t.Roots, t.SequenceCatalog = session.CommittedRoots(e.Catalog.State, &e.Storage), e.Catalog.State
	return nil
}

func (s *StatementScope) readOnlyUtilityEligibility(stmt parser.Statement) error {
	if session.ReadOnlyExempt(stmt) {
		return nil
	}
	t := s.Owner
	t.Mu.Lock()
	defer t.Mu.Unlock()
	return session.ReadOnlyRefusal(stmt, t.ReadOnly)
}
func (s *StatementScope) executionEligibility(stmt parser.Statement) error {
	if !session.ExplicitUtility(stmt) || session.TransactionalSchemaUtility(stmt) || transactionalIndexMaintenance(stmt) || transactionalRoleUtility(stmt) || s.partitionIndexTransaction(stmt) {
		return nil
	}
	if index, ok := stmt.(*parser.CreateIndex); ok && index.Concurrent.Character == 0 {
		return nil
	}
	t := s.Owner
	t.Mu.Lock()
	defer t.Mu.Unlock()
	return session.ExplicitUtilityRefusal(stmt, t.State, t.InBlock())
}

// Only shipped metadata maintenance is transactional here. CREATE and REINDEX
// retain their own admission boundaries.
func transactionalIndexMaintenance(stmt parser.Statement) bool {
	switch s := stmt.(type) {
	case *parser.DropIndex:
		return s.Concurrent.Character == 0
	case *parser.AlterIndex:
		return s.Action == "rename" || s.Action == "set" || s.Action == "reset"
	}
	return false
}

func transactionalRoleUtility(stmt parser.Statement) bool {
	switch stmt.(type) {
	case *parser.CreateRole, *parser.AlterRole, *parser.DropRole, *parser.RoleMembership, *parser.SessionAuthorization:
		return true
	}
	return false
}
