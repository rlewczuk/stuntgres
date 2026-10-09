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
	default:
		return "read committed"
	}
}
func (t *transactionOwner) syncCharacteristics() {
	settings := t.session.settings
	settings.mu.Lock()
	settings.currentIsolation = isolationText(t.isolation)
	settings.currentReadOnly = t.readOnly
	settings.mu.Unlock()
}
func validateModes(modes []parser.TransactionMode) error {
	for _, mode := range modes {
		if mode.Unsupported || mode.Isolation == parser.IsolationSerializable {
			return sqlError("0A000", "transaction mode is unsupported")
		}
	}
	return nil
}
func (t *transactionOwner) assignModes(modes []parser.TransactionMode) error {
	if err := validateModes(modes); err != nil {
		return err
	}
	isolation, readOnly := t.isolation, t.readOnly
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
	}
	t.isolation, t.readOnly = isolation, readOnly
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
	if t.readOnly {
		return "on", nil
	}
	return "off", nil
}
func (s *StatementScope) executeCharacteristics(stmt *parser.TransactionCharacteristics, count int) (Result, error) {
	if count != 0 {
		return Result{}, errors.New("stuntgres: transaction characteristics take no arguments")
	}
	if err := validateModes(stmt.Modes); err != nil {
		return Result{}, err
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
	if t.state == TxImplicit {
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
	case *parser.Select, *parser.Insert, *parser.Update, *parser.Delete, *parser.SettingCall:
	default:
		return nil
	}
	t, e := s.owner, s.op.session.Engine()
	t.mu.Lock()
	defer t.mu.Unlock()
	needsProjection := func(roots map[ObjectAddress]transactionRoot) bool {
		if t.isolation != RepeatableRead || t.catalog == nil {
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
	if !t.snapshotSet && t.isolation == RepeatableRead {
		roots = make(map[ObjectAddress]transactionRoot, len(e.storage))
		for address, storage := range e.storage {
			if relation, ok := e.state.descriptors[address].(*relationDescriptor); ok {
				roots[address] = transactionRoot{storage.versions, storage.ends, storage, relation}
			}
		}
		frontier = e.commitSeq
	}
	if !needsProjection(roots) {
		t.roots, t.frontier = roots, frontier
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
		t.snapshotSet = true
	}
	session.mu.Unlock()
	e.mu.Unlock()
	return err
}
func modifyingTag(stmt parser.Statement) string {
	switch stmt.(type) {
	case *parser.CreateIndex:
		return "CREATE INDEX"
	case *parser.CreateView:
		return "CREATE VIEW"
	case *parser.AlterView:
		return "ALTER VIEW"
	case *parser.DropView:
		return "DROP VIEW"
	case *parser.Select:
		if sel := stmt.(*parser.Select); sel.Locking != nil {
			return "SELECT " + sel.Locking.Mode
		}
	case *parser.CreateSequence:
		return "CREATE SEQUENCE"
	case *parser.AlterSequence:
		return "ALTER SEQUENCE"
	case *parser.DropSequence:
		return "DROP SEQUENCE"
	case *parser.Insert:
		return "INSERT"
	case *parser.Update:
		return "UPDATE"
	case *parser.Delete:
		return "DELETE"
	case *parser.CreateTable:
		return "CREATE TABLE"
	case *parser.DropTable:
		return "DROP TABLE"
	case *parser.CreateEnum:
		return "CREATE TYPE"
	case *parser.CreateDomain:
		return "CREATE DOMAIN"
	case *parser.DropDomain:
		return "DROP DOMAIN"
	case *parser.AlterTableSetSchema, *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableDefault, *parser.AlterTable:
		return "ALTER TABLE"
	case *parser.CreateSchema:
		return "CREATE SCHEMA"
	case *parser.AlterSchema:
		return "ALTER SCHEMA"
	case *parser.DropSchema:
		return "DROP SCHEMA"
	case *parser.CreateRole:
		return "CREATE ROLE"
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
	case *parser.Select, *parser.Insert, *parser.Update, *parser.Delete:
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
	t := s.owner
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == TxActive {
		return sqlError("0A000", "statement is unsupported in an explicit transaction")
	}
	return nil
}
func explicitUtility(stmt parser.Statement) bool {
	if c, ok := stmt.(*parser.Comment); ok && c.Kind == parser.CommentSequence {
		return false
	}
	switch stmt.(type) {
	case *parser.CreateIndex, *parser.CreateView, *parser.AlterView, *parser.DropView:
		return true
	case *parser.CreateEnum, *parser.CreateDomain, *parser.DropDomain:
		return true
	case *parser.Comment, *parser.AlterTableSetSchema, *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableDefault, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateTable, *parser.DropTable, *parser.CreateRole, *parser.DropRole, *parser.SessionAuthorization:
		return true
	case *parser.RoleSelection:
		return true
	}
	return false
}
func (s *StatementScope) repeatableConflict(head rowVersion, end *deletionRoot) error {
	if s.owner.isolation != RepeatableRead || head.born == 0 {
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
