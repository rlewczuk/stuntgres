package engine

import (
	"errors"
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
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
	isolation, readOnly, deferrable := t.Isolation, t.ReadOnly, t.Deferrable
	for _, mode := range modes {
		if mode.Isolation != parser.IsolationDefault {
			next := session.Isolation(mode.Isolation)
			if t.SnapshotSet && next != isolation {
				return sqlError("25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query")
			}
			if len(t.Savepoints) != 0 && next != isolation {
				return sqlError("25001", "SET TRANSACTION ISOLATION LEVEL must not be called in a subtransaction")
			}
			isolation = next
		}
		if mode.AccessSet {
			if len(t.Savepoints) != 0 && readOnly && !mode.ReadOnly {
				return sqlError("25001", "cannot set transaction read-write mode inside a read-only transaction")
			}
			if t.SnapshotSet && readOnly && !mode.ReadOnly {
				return sqlError("25001", "transaction read-write mode must be set before any query")
			}
			readOnly = mode.ReadOnly
		}
		if mode.DeferrableSet {
			if len(t.Savepoints) != 0 {
				return sqlError("25001", "SET TRANSACTION [NOT] DEFERRABLE cannot be called within a subtransaction")
			}
			if t.SnapshotSet {
				return sqlError("25001", "SET TRANSACTION [NOT] DEFERRABLE must be called before any query")
			}
			deferrable = mode.Deferrable
		}
	}
	t.Isolation, t.ReadOnly = isolation, readOnly
	t.Deferrable = deferrable
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
	mode := parser.TransactionMode{}
	if d.Name == "transaction_isolation" {
		mode.Isolation = parser.TransactionIsolation(session.IsolationFromText(d.Format(value)))
	} else if d.Name == "transaction_deferrable" {
		mode.DeferrableSet, mode.Deferrable = true, d.Format(value) == "on"
	} else {
		mode.AccessSet = true
		mode.ReadOnly = d.Format(value) == "on"
	}
	if err := assignModes(t, []parser.TransactionMode{mode}); err != nil {
		return "", err
	}
	if d.Name == "transaction_isolation" {
		return session.IsolationText(t.Isolation), nil
	}
	if d.Name == "transaction_deferrable" {
		if t.Deferrable {
			return "on", nil
		}
		return "off", nil
	}
	if t.ReadOnly {
		return "on", nil
	}
	return "off", nil
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
func (s *StatementScope) snapshotPhase(stmt parser.Statement) error {
	switch stmt.(type) {
	case *parser.Copy, *parser.Select, *parser.DeclareCursor, *parser.FetchCursor, *parser.CreateQueryTable, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge, *parser.SettingCall:
	default:
		return nil
	}
	return s.snapshotDataPhase()
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
	if t.SnapshotSet && !needsProjection(t.Roots) {
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
	roots, frontier := t.Roots, t.Frontier
	sequenceCatalog := t.SequenceCatalog
	if !t.SnapshotSet && t.Isolation.Stable() {
		if _, err := e.visibilitySnapshotLocked(t.Ref, false); err != nil {
			sess.Mu.Unlock()
			e.mu.Unlock()
			return err
		}
		roots = make(map[ids.ObjectAddress]visibility.Root, len(e.storage.Relations))
		for address, storage := range e.storage.Relations {
			if relation, ok := e.catalog.state.Relation(address); ok {
				roots[address] = visibility.Root{storage.Versions, storage.Ends, storage, relation}
			}
		}
		frontier = e.visibility.CommitSeq()
		sequenceCatalog = e.catalog.state
	}
	if !needsProjection(roots) {
		t.Roots, t.Frontier = roots, frontier
		t.SequenceCatalog = sequenceCatalog
		t.SnapshotSet = true
		sess.Mu.Unlock()
		e.mu.Unlock()
		return nil
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
	}
	sess.Mu.Unlock()
	e.mu.Unlock()
	return err
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
func (s *StatementScope) repeatableConflict(head storage.RowVersion, end *storage.DeletionRoot) error {
	if !s.owner.Isolation.Stable() || head.Born == 0 {
		return nil
	}
	// The snapshot's predecessor was replaced before its successor was deleted.
	// PostgreSQL reports that first update, rather than the successor's tombstone.
	if head.Born > s.owner.Frontier {
		return sqlError("40001", "could not serialize access due to concurrent update")
	}
	if end != nil {
		if tombstone, ok := end.Rows[head.Row]; ok && tombstone.Version == head.Version && tombstone.End > s.owner.Frontier {
			return sqlError("40001", "could not serialize access due to concurrent delete")
		}
	}
	return nil
}
