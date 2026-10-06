package engine

import (
	"errors"
	"fmt"
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
			isolation = next
		}
		if mode.AccessSet {
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
	case *parser.Select, *parser.Insert, *parser.Update, *parser.Delete, *parser.IdentitySelect, *parser.SettingCall:
	default:
		return nil
	}
	t, e := s.owner, s.op.session.Engine()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.snapshotSet {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s.op.session.mu.Lock()
	defer s.op.session.mu.Unlock()
	if err := publicationOperationLocked(e, s.op); err != nil {
		return err
	}
	if t.ref != s.txRef || t.ref.epoch != e.state.epoch {
		return ErrCatalogReplaced
	}
	if t.isolation == RepeatableRead {
		roots := make(map[ObjectAddress]transactionRoot, len(e.storage))
		for address, storage := range e.storage {
			relation, ok := e.state.descriptors[address].(*relationDescriptor)
			if ok {
				roots[address] = transactionRoot{storage.versions, storage.ends, storage, relation}
			}
		}
		t.roots = roots
		t.frontier = e.commitSeq
	}
	t.snapshotSet = true
	return nil
}
func modifyingTag(stmt parser.Statement) string {
	switch stmt.(type) {
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
	case *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableNotNull, *parser.AlterTableDropColumn:
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
	case *parser.Insert, *parser.Update, *parser.Delete:
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
	case *parser.Comment, *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableNotNull, *parser.AlterTableDropColumn, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateTable, *parser.DropTable, *parser.CreateRole, *parser.DropRole, *parser.SessionAuthorization:
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
