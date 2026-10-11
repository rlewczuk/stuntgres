package engine

import (
	"errors"
	"fmt"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type transactionRoot struct {
	versions []rowVersion
	ends     *deletionRoot
	storage  *relationStorage
	relation *relationDescriptor
}

func isolationFromText(text string) Isolation {
	switch text {
	case "read uncommitted":
		return ReadUncommitted
	case "repeatable read":
		return RepeatableRead
	case "serializable":
		return Serializable
	default:
		return ReadCommitted
	}
}
func isolationText(level Isolation) string {
	switch level {
	case ReadUncommitted:
		return "read uncommitted"
	case RepeatableRead:
		return "repeatable read"
	case Serializable:
		return "serializable"
	default:
		return "read committed"
	}
}
func (t *transactionOwner) syncCharacteristics() {
	settings := t.session.settings
	settings.mu.Lock()
	settings.currentIsolation = isolationText(t.isolation)
	settings.currentReadOnly = t.readOnly
	settings.currentDeferrable = t.deferrable
	settings.mu.Unlock()
	e := t.session.Engine()
	e.mu.Lock()
	if !t.snapshotSet && (t.state == TxActive || t.state == TxImplicit) {
		e.enrollSerializableLocked(t.ref, t.isolation)
	}
	e.mu.Unlock()
}
func (t *transactionOwner) assignModes(modes []parser.TransactionMode) error {
	isolation, readOnly, deferrable := t.isolation, t.readOnly, t.deferrable
	for _, mode := range modes {
		if mode.Isolation != parser.IsolationDefault {
			next := Isolation(mode.Isolation)
			if t.snapshotSet && next != isolation {
				return sqlError("25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query")
			}
			if len(t.savepoints) != 0 && next != isolation {
				return sqlError("25001", "SET TRANSACTION ISOLATION LEVEL must not be called in a subtransaction")
			}
			isolation = next
		}
		if mode.AccessSet {
			if len(t.savepoints) != 0 && readOnly && !mode.ReadOnly {
				return sqlError("25001", "cannot set transaction read-write mode inside a read-only transaction")
			}
			if t.snapshotSet && readOnly && !mode.ReadOnly {
				return sqlError("25001", "transaction read-write mode must be set before any query")
			}
			readOnly = mode.ReadOnly
		}
		if mode.DeferrableSet {
			if len(t.savepoints) != 0 {
				return sqlError("25001", "SET TRANSACTION [NOT] DEFERRABLE cannot be called within a subtransaction")
			}
			if t.snapshotSet {
				return sqlError("25001", "SET TRANSACTION [NOT] DEFERRABLE must be called before any query")
			}
			deferrable = mode.Deferrable
		}
	}
	t.isolation, t.readOnly = isolation, readOnly
	t.deferrable = deferrable
	t.syncCharacteristics()
	return nil
}
func (e *Engine) applyCurrentCharacteristic(op *Operation, d SettingDefinition, input *string) (string, error) {
	if input == nil {
		return "", sqlError("0A000", fmt.Sprintf(`parameter "%s" cannot be reset`, d.Name))
	}
	value, err := d.parse(*input)
	if err != nil {
		return "", err
	}
	t := op.session.transactions
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := e.endPreflight(op); err != nil {
		return "", err
	}
	mode := parser.TransactionMode{}
	if d.Name == "transaction_isolation" {
		mode.Isolation = parser.TransactionIsolation(isolationFromText(d.format(value)))
	} else if d.Name == "transaction_deferrable" {
		mode.DeferrableSet, mode.Deferrable = true, d.format(value) == "on"
	} else {
		mode.AccessSet = true
		mode.ReadOnly = d.format(value) == "on"
	}
	if err := t.assignModes([]parser.TransactionMode{mode}); err != nil {
		return "", err
	}
	if d.Name == "transaction_isolation" {
		return isolationText(t.isolation), nil
	}
	if d.Name == "transaction_deferrable" {
		if t.deferrable {
			return "on", nil
		}
		return "off", nil
	}
	if t.readOnly {
		return "on", nil
	}
	return "off", nil
}
func (s *StatementScope) executeCharacteristics(stmt *parser.TransactionCharacteristics, count int) (Result, error) {
	if count != 0 {
		return Result{}, errors.New("stuntgres: transaction characteristics take no arguments")
	}
	e, t := s.op.session.Engine(), s.owner
	if !s.op.commandAdmission {
		s.op.snapshotNoticeThreshold()
	}
	if stmt.Session {
		for _, mode := range stmt.Modes {
			name, value := "", ""
			if mode.Isolation != parser.IsolationDefault {
				name, value = "default_transaction_isolation", isolationText(Isolation(mode.Isolation))
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
			d := e.settingsDefinitions[name]
			e.mu.Unlock()
			if _, err := e.applySetting(s.op, t.session.settings, d, &value, false); err != nil {
				return Result{}, err
			}
		}
		s.completed = true
		return Result{}, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := e.endPreflight(s.op); err != nil {
		return Result{}, err
	}
	if !t.inBlock() {
		s.op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET TRANSACTION can only be used in transaction blocks"})
	}
	err := t.assignModes(stmt.Modes)
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
	t, e := s.owner, s.op.session.Engine()
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := s.acquireSerializableSnapshot(); err != nil {
		return err
	}
	needsProjection := func(roots map[ObjectAddress]transactionRoot) bool {
		if !stableIsolation(t.isolation) || t.catalog == nil {
			return false
		}
		for address, shape := range t.catalog.storage {
			if root, exists := roots[address]; exists && !shape.hasSnapshot && !slices.Equal(root.relation.attributes, shape.relation.attributes) {
				return true
			}
		}
		return false
	}
	if t.snapshotSet && !needsProjection(t.roots) {
		return nil
	}
	session := s.op.session
	e.mu.Lock()
	session.mu.Lock()
	check := func() error {
		if err := publicationOperationLocked(e, s.op); err != nil {
			return err
		}
		if t.ref != s.txRef || t.ref.epoch != e.state.epoch {
			return ErrCatalogReplaced
		}
		return nil
	}
	if err := check(); err != nil {
		session.mu.Unlock()
		e.mu.Unlock()
		return err
	}
	roots, frontier := t.roots, t.frontier
	sequenceCatalog := t.sequenceCatalog
	if !t.snapshotSet && stableIsolation(t.isolation) {
		if _, err := e.visibilitySnapshotLocked(t.ref, false); err != nil {
			session.mu.Unlock()
			e.mu.Unlock()
			return err
		}
		roots = make(map[ObjectAddress]transactionRoot, len(e.storage))
		for address, storage := range e.storage {
			if relation, ok := e.state.descriptors[address].(*relationDescriptor); ok {
				roots[address] = transactionRoot{storage.versions, storage.ends, storage, relation}
			}
		}
		frontier = e.commitSeq
		sequenceCatalog = e.state
	}
	if !needsProjection(roots) {
		t.roots, t.frontier = roots, frontier
		t.sequenceCatalog = sequenceCatalog
		t.snapshotSet = true
		session.mu.Unlock()
		e.mu.Unlock()
		return nil
	}
	catalog := t.catalog
	session.mu.Unlock()
	e.mu.Unlock()
	// Raw roots remain the committed authority. Dense private projections are
	// prepared outside metadata locks and attached only after final admission.
	projected, err := projectPrivateSnapshotRoots(s.op.Context(), catalog, roots)
	if err != nil {
		return err
	}
	e.mu.Lock()
	session.mu.Lock()
	err = check()
	if err == nil {
		t.catalog = projected
		t.roots, t.frontier = roots, frontier
		t.sequenceCatalog = sequenceCatalog
		t.snapshotSet = true
	}
	session.mu.Unlock()
	e.mu.Unlock()
	return err
}
func modifyingTag(stmt parser.Statement) string {
	switch stmt.(type) {
	case *parser.Truncate:
		return "TRUNCATE TABLE"
	case *parser.RefreshMaterializedView:
		return "REFRESH MATERIALIZED VIEW"
	case *parser.CreatePolicy:
		return "CREATE POLICY"
	case *parser.AlterPolicy:
		return "ALTER POLICY"
	case *parser.DropPolicy:
		return "DROP POLICY"
	case *parser.Copy:
		if stmt.(*parser.Copy).From {
			return "COPY FROM"
		}
	case *parser.CreateIndex:
		return "CREATE INDEX"
	case *parser.DropIndex:
		return "DROP INDEX"
	case *parser.AlterIndex:
		return "ALTER INDEX"
	case *parser.Reindex:
		return "REINDEX"
	case *parser.CreateView:
		return "CREATE VIEW"
	case *parser.AlterView:
		if stmt.(*parser.AlterView).Materialized {
			return "ALTER MATERIALIZED VIEW"
		}
		return "ALTER VIEW"
	case *parser.DropView:
		if stmt.(*parser.DropView).Materialized {
			return "DROP MATERIALIZED VIEW"
		}
		return "DROP VIEW"
	case *parser.Select:
		if sel := stmt.(*parser.Select); sel.Locking != nil {
			return "SELECT " + sel.Locking[0].Mode
		}
	case *parser.CreateSequence:
		return "CREATE SEQUENCE"
	case *parser.AlterSequence:
		return "ALTER SEQUENCE"
	case *parser.DropSequence:
		return "DROP SEQUENCE"
	case *parser.Insert:
		return "INSERT"
	case *parser.Merge:
		return "MERGE"
	case *parser.Update:
		return "UPDATE"
	case *parser.Delete:
		return "DELETE"
	case *parser.CreateQueryTable:
		if stmt.(*parser.CreateQueryTable).Form == parser.CreateMaterializedView {
			return "CREATE MATERIALIZED VIEW"
		}
		if stmt.(*parser.CreateQueryTable).Form == parser.SelectInto {
			return "SELECT INTO"
		}
		return "CREATE TABLE AS"
	case *parser.CreateTable:
		return "CREATE TABLE"
	case *parser.DropTable:
		return "DROP TABLE"
	case *parser.CreateComposite, *parser.CreateEnum:
		return "CREATE TYPE"
	case *parser.AlterEnum, *parser.AlterComposite:
		return "ALTER TYPE"
	case *parser.DropComposite:
		return "DROP TYPE"
	case *parser.CreateDomain:
		return "CREATE DOMAIN"
	case *parser.DropDomain:
		return "DROP DOMAIN"
	case *parser.CreateCollation:
		return "CREATE COLLATION"
	case *parser.AlterCollation:
		return "ALTER COLLATION"
	case *parser.DropCollation:
		return "DROP COLLATION"
	case *parser.AlterTableOf, *parser.AlterTableSetSchema, *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableRenameConstraint, *parser.AlterTableDefault, *parser.AlterTable:
		return "ALTER TABLE"
	case *parser.CreateExtension:
		return "CREATE EXTENSION"
	case *parser.CreateSchema:
		return "CREATE SCHEMA"
	case *parser.AlterSchema:
		return "ALTER SCHEMA"
	case *parser.DropSchema:
		return "DROP SCHEMA"
	case *parser.CreateRole:
		return "CREATE ROLE"
	case *parser.AlterDefaultPrivileges:
		return "ALTER DEFAULT PRIVILEGES"
	case *parser.ObjectGrant:
		if stmt.(*parser.ObjectGrant).Revoke {
			return "REVOKE"
		}
		return "GRANT"
	case *parser.RoleMembership:
		m := stmt.(*parser.RoleMembership)
		if m.Alias {
			return "ALTER ROLE"
		}
		if m.Revoke {
			return "REVOKE ROLE"
		}
		return "GRANT ROLE"
	case *parser.AlterRole:
		return "ALTER ROLE"
	case *parser.DropRole:
		return "DROP ROLE"
	case *parser.Comment:
		return "COMMENT"
	}
	return ""
}
func (s *StatementScope) readOnlyUtilityEligibility(stmt parser.Statement) error {
	t := s.owner
	switch stmt.(type) {
	case *parser.Copy, *parser.Select, *parser.DeclareCursor, *parser.FetchCursor, *parser.CloseCursor, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if tag := modifyingTag(stmt); t.readOnly && tag != "" {
		return sqlError("25006", "cannot execute "+tag+" in a read-only transaction")
	}
	return nil
}
func (s *StatementScope) executionEligibility(stmt parser.Statement) error {
	if !explicitUtility(stmt) {
		return nil
	}
	switch stmt.(type) {
	case *parser.CreateTable, *parser.DropTable, *parser.AlterTableRenameConstraint:
		// Resolved destinations are checked by the existing table executor,
		// after read-only utility admission and before any catalog mutation.
		return nil
	}
	t := s.owner
	t.mu.Lock()
	defer t.mu.Unlock()
	if index, ok := stmt.(*parser.CreateIndex); ok && index.Concurrent.Character != 0 && t.inBlock() {
		return sqlError("25001", "CREATE INDEX CONCURRENTLY cannot run inside a transaction block")
	}
	if t.state == TxActive {
		return sqlError("0A000", "statement is unsupported in an explicit transaction")
	}
	return nil
}
func explicitUtility(stmt parser.Statement) bool {
	if c, ok := stmt.(*parser.Comment); ok && c.Kind == parser.CommentSequence {
		return false
	}
	if grant, ok := stmt.(*parser.ObjectGrant); ok && grant.Kind == "database" {
		return false
	}
	switch stmt.(type) {
	case *parser.RefreshMaterializedView, *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy:
		return true
	case *parser.CreateIndex, *parser.DropIndex, *parser.AlterIndex, *parser.Reindex, *parser.CreateView, *parser.AlterView, *parser.DropView:
		return true
	case *parser.CreateComposite, *parser.AlterComposite, *parser.DropComposite, *parser.AlterTableOf, *parser.CreateDomain, *parser.DropDomain, *parser.CreateQueryTable, *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation:
		return true
	case *parser.Comment, *parser.AlterTableSetSchema, *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableRenameConstraint, *parser.AlterTableDefault, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateTable, *parser.DropTable, *parser.CreateRole, *parser.AlterRole, *parser.DropRole, *parser.SessionAuthorization:
		return true
	case *parser.RoleMembership, *parser.ObjectGrant, *parser.AlterDefaultPrivileges:
		return true
	}
	return false
}
func (s *StatementScope) repeatableConflict(head rowVersion, end *deletionRoot) error {
	if !stableIsolation(s.owner.isolation) || head.born == 0 {
		return nil
	}
	// The snapshot's predecessor was replaced before its successor was deleted.
	// PostgreSQL reports that first update, rather than the successor's tombstone.
	if head.born > s.owner.frontier {
		return sqlError("40001", "could not serialize access due to concurrent update")
	}
	if end != nil {
		if tombstone, ok := end.rows[head.row]; ok && tombstone.version == head.version && tombstone.end > s.owner.frontier {
			return sqlError("40001", "could not serialize access due to concurrent delete")
		}
	}
	return nil
}

// Only index utilities have a syntax-only Describe and an early Execute gate.
// Other utility admission precedence is deliberately unchanged.
func earlyIndexUtility(stmt parser.Statement) bool {
	switch stmt.(type) {
	case *parser.CreateIndex, *parser.DropIndex, *parser.AlterIndex, *parser.Reindex:
		return true
	}
	return false
}
