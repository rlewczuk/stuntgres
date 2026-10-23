package engine

import (
	"errors"
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/visibility"
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
	e := engineOf(t.Session)
	e.mu.Lock()
	if !t.SnapshotSet && (t.State == session.TxActive || t.State == session.TxImplicit) {
		e.enrollSerializableLocked(t.Ref, t.Isolation)
	}
	e.mu.Unlock()
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
		return "", sqlError("0A000", fmt.Sprintf(`parameter "%s" cannot be reset`, d.Name))
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
	e, t := s.engine(), s.owner
	if !s.op.CommandAdmission {
		s.op.SnapshotNoticeThreshold()
	}
	if stmt.Session {
		for _, mode := range stmt.Modes {
			name, value := "", ""
			if mode.Isolation != parser.IsolationDefault {
				name, value = "default_transaction_isolation", session.IsolationText(session.Isolation(mode.Isolation))
			}
			if mode.AccessSet {
				name, value = "default_transaction_read_only", "off"
				if mode.ReadOnly {
					value = "on"
				}
			}
			if mode.DeferrableSet {
				name, value = "default_transaction_deferrable", "off"
				if mode.Deferrable {
					value = "on"
				}
			}
			e.mu.Lock()
			d := e.settings.Definitions()[name]
			e.mu.Unlock()
			if _, err := e.applySetting(s.op, t.Session.Settings, d, &value, false); err != nil {
				return Result{}, err
			}
		}
		s.completed = true
		return Result{}, nil
	}
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if err := e.endPreflight(s.op); err != nil {
		return Result{}, err
	}
	if !t.InBlock() {
		s.op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET TRANSACTION can only be used in transaction blocks"})
	}
	err := assignModes(t, stmt.Modes)
	if err == nil {
		s.completed = true
	}
	return Result{}, err
}

// Analysis can establish a transaction-stable snapshot, but RC analysis must
// not select the later command's execution frontier.
func (s *StatementScope) snapshotAnalysisPhase(stmt parser.Statement) error {
	if !s.owner.Isolation.Stable() {
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
	switch stmt.(type) {
	case *parser.Copy, *parser.Select, *parser.DeclareCursor, *parser.CreateQueryTable, *parser.RefreshMaterializedView, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge, *parser.SettingCall:
		return s.snapshotDataPhase()
	default:
		return nil
	}
}

// Fastpath has no SQL AST but obtains the same transaction snapshot before
// reading its function identity or invoking argument/result codecs.
func (s *StatementScope) snapshotDataPhase() error {
	t, e := s.owner, s.engine()
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if err := s.acquireSerializableSnapshot(); err != nil {
		return err
	}
	needsProjection := func(roots map[ids.ObjectAddress]visibility.Root) bool {
		if !t.Isolation.Stable() || t.Catalog == nil {
			return false
		}
		for address, shape := range t.Catalog.Storage {
			if root, exists := roots[address]; exists && !shape.HasSnapshot && !slices.Equal(root.Relation.Attributes, shape.Relation.Attributes) {
				return true
			}
		}
		return false
	}
	if t.SnapshotSet && !needsProjection(t.Roots) && s.currentSnapshot != nil {
		return nil
	}
	sess := s.op.Session
	e.mu.Lock()
	sess.Mu.Lock()
	check := func() error {
		if err := publicationOperationLocked(e, s.op); err != nil {
			return err
		}
		if t.Ref != s.txRef || t.Ref.Epoch() != e.catalog.state.Epoch() {
			return ids.ErrCatalogReplaced
		}
		return nil
	}
	if err := check(); err != nil {
		sess.Mu.Unlock()
		e.mu.Unlock()
		return err
	}
	if !t.SnapshotSet && t.Isolation.Stable() {
		if _, err := e.visibilitySnapshotLocked(t.Ref, false); err != nil {
			sess.Mu.Unlock()
			e.mu.Unlock()
			return err
		}
		if err := e.retainTransactionSnapshotLocked(t); err != nil {
			sess.Mu.Unlock()
			e.mu.Unlock()
			return err
		}
	}
	roots, frontier := t.Roots, t.Frontier
	sequenceCatalog := t.SequenceCatalog
	if !needsProjection(roots) {
		t.Roots, t.Frontier = roots, frontier
		t.SequenceCatalog = sequenceCatalog
		t.SnapshotSet = true
		err := e.captureStatementSnapshotLocked(s)
		sess.Mu.Unlock()
		e.mu.Unlock()
		return err
	}
	catalog := t.Catalog
	sess.Mu.Unlock()
	e.mu.Unlock()
	// Raw roots remain the committed authority. Dense private projections are
	// prepared outside metadata locks and attached only after final admission.
	projected, err := session.ProjectPrivateSnapshotRoots(s.op.Context(), catalog, roots)
	if err != nil {
		return err
	}
	e.mu.Lock()
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
	e.mu.Unlock()
	return err
}

// captureStatementSnapshotLocked installs the command's immutable public value
// and tuple frontier together. Engine.mu is held, and stable owner acquisition
// (including any SSI safe-snapshot wait) has already completed.
func (e *Engine) captureStatementSnapshotLocked(s *StatementScope) error {
	if s.currentSnapshot != nil {
		return nil
	}
	t := s.owner
	if t.Isolation.Stable() {
		if !t.SnapshotSet || t.CurrentSnapshot == nil {
			return sqlError("55000", "no active transaction snapshot")
		}
		s.currentSnapshot, s.snapshotFrontier = t.CurrentSnapshot, t.Frontier
		return nil
	}
	snapshot, err := e.visibility.CurrentSnapshotLocked(t.Ref)
	if err != nil {
		return err
	}
	s.currentSnapshot, s.snapshotFrontier = snapshot, e.visibility.CommitSeq()
	// RC retains only the command pair, but acquiring it still closes the
	// transaction-characteristics window for the rest of this transaction.
	t.SnapshotSet = true
	return nil
}

// retainTransactionSnapshotLocked selects all stable owner state in one
// Engine.mu window. SSI uses the same acquisition for each safe candidate;
// SnapshotSet is installed only after that candidate/projection is accepted.
func (e *Engine) retainTransactionSnapshotLocked(t *session.TransactionOwner) error {
	snapshot, err := e.visibility.CurrentSnapshotLocked(t.Ref)
	if err != nil {
		return err
	}
	roots := make(map[ids.ObjectAddress]visibility.Root, len(e.storage.Relations))
	for address, storage := range e.storage.Relations {
		if relation, ok := e.catalog.state.Relation(address); ok {
			roots[address] = visibility.Root{storage.Versions, storage.Ends, storage, relation}
		}
	}
	t.CurrentSnapshot, t.Frontier = snapshot, e.visibility.CommitSeq()
	t.Roots, t.SequenceCatalog = roots, e.catalog.state
	return nil
}

func (s *StatementScope) readOnlyUtilityEligibility(stmt parser.Statement) error {
	t := s.owner
	switch stmt.(type) {
	case *parser.Copy, *parser.Select, *parser.DeclareCursor, *parser.FetchCursor, *parser.CloseCursor, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		return nil
	}
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if tag := session.ModifyingTag(stmt); t.ReadOnly && tag != "" {
		return sqlError("25006", "cannot execute "+tag+" in a read-only transaction")
	}
	return nil
}
func (s *StatementScope) executionEligibility(stmt parser.Statement) error {
	if !session.ExplicitUtility(stmt) || s.partitionIndexTransaction(stmt) {
		return nil
	}
	switch stmt.(type) {
	case *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition:
		// Partition edge changes use the canonical catalog overlay in every owner mode.
		return nil
	case *parser.CreateTable, *parser.DropTable, *parser.AlterTableRenameConstraint:
		// Resolved destinations are checked by the existing table executor,
		// after read-only utility admission and before any catalog mutation.
		return nil
	}
	t := s.owner
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if _, ok := stmt.(*parser.CreateDatabaseStmt); ok {
		if t.InBlock() {
			return sqlError("25001", "CREATE DATABASE cannot run inside a transaction block")
		}
		return nil
	}
	if _, ok := stmt.(*parser.CreateTablespace); ok {
		if t.InBlock() {
			return sqlError("25001", "CREATE TABLESPACE cannot run inside a transaction block")
		}
		return nil
	}
	if index, ok := stmt.(*parser.CreateIndex); ok && index.Concurrent.Character != 0 && t.InBlock() {
		return sqlError("25001", "CREATE INDEX CONCURRENTLY cannot run inside a transaction block")
	}
	if t.State == session.TxActive {
		return sqlError("0A000", "statement is unsupported in an explicit transaction")
	}
	return nil
}
