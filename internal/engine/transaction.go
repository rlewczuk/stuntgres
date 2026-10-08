package engine

import (
	"context"
	"errors"
	"sync"
	"time"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type TxID uint64
type CommandID uint64
type CommitSeq uint64
type RowID uint64
type VersionID uint64
type TxState uint8

const (
	TxIdle TxState = iota
	TxImplicit
	TxActive
	TxAborted
)

type Isolation uint8

const (
	DefaultIsolation Isolation = iota
	ReadCommitted
	ReadUncommitted
	RepeatableRead
	Serializable
)

type TxOptions struct {
	Isolation Isolation
	ReadOnly  bool
}

type TxRef struct {
	owner      *Engine
	session    SessionToken
	id         TxID
	epoch      CatalogEpoch
	attachment *transactionOwner
}

func (r TxRef) ID() TxID { return r.id }

type EndResult struct{ Committed bool }

type rowVersion struct {
	row     RowID
	version VersionID
	born    CommitSeq
	// base is the committed predecessor for a private replacement; zero is an insert.
	base   VersionID
	values []sqltype.Value
}

type transactionOwner struct {
	admission      sync.Mutex
	ticket         *relationTicket
	mu             sync.Mutex
	session        *Session
	state          TxState
	ref            TxRef
	next           TxID
	command        CommandID
	writes         map[ObjectAddress][]rowVersion
	deletes        map[ObjectAddress]*privateDeletionRoot
	start          time.Time
	isolation      Isolation
	readOnly       bool
	entryIsolation Isolation
	entryReadOnly  bool
	snapshotSet    bool
	frontier       CommitSeq
	roots          map[ObjectAddress]transactionRoot
	sequences      *sequenceChanges
	savepoints     []savepointMark
}

func (t *transactionOwner) clear() {
	t.state = TxIdle
	t.ref = TxRef{}
	t.command = 0
	t.writes = nil
	t.deletes = nil
	t.start = time.Time{}
	t.snapshotSet = false
	t.frontier = 0
	t.roots = nil
	t.sequences = nil
	clear(t.savepoints)
	t.savepoints = nil
}

func (t *transactionOwner) EndSession(token SessionToken, _ EndReason) error {
	t.admission.Lock()
	defer t.admission.Unlock()
	t.mu.Lock()
	if token != t.session.token {
		t.mu.Unlock()
		return errors.New("stuntgres: foreign transaction session")
	}
	t.session.settings.finishTransaction(false)
	t.clear()
	ticket := t.ticket
	t.ticket = nil
	t.mu.Unlock()
	t.session.Engine().locks.end(ticket)
	t.session.Engine().endSequenceSession(t.session)
	return nil
}

func (s *Session) TransactionState() TxState {
	t := s.transactions
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

func (e *Engine) beginLocked(op *Operation, t *transactionOwner, state TxState) error {
	if t.next == TxID(^uint64(0)) {
		return errors.New("stuntgres: transaction identity exhausted")
	}
	session := op.session
	e.mu.Lock()
	defer e.mu.Unlock()
	settings := session.settings
	settings.mu.Lock()
	defer settings.mu.Unlock()
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return err
	}
	if settings.value("default_transaction_isolation") == "serializable" {
		return sqlError("0A000", "transaction mode is unsupported")
	}
	t.next++
	t.ref = TxRef{owner: e, session: t.session.token, id: t.next, epoch: e.state.epoch, attachment: t}
	t.state = state
	t.isolation = isolationFromText(settings.value("default_transaction_isolation"))
	t.readOnly = settings.value("default_transaction_read_only") == "on"
	t.entryIsolation, t.entryReadOnly = t.isolation, t.readOnly
	settings.currentIsolation = isolationText(t.isolation)
	settings.currentReadOnly = t.readOnly
	t.start = time.Now().UTC()
	return nil
}

func (e *Engine) Begin(op *Operation, options TxOptions) (TxRef, error) {
	if options.Isolation > RepeatableRead {
		return TxRef{}, errors.New("stuntgres: unsupported transaction isolation level")
	}
	if err := e.transactionAdmission(op); err != nil {
		return TxRef{}, err
	}
	t := op.session.transactions
	t.admission.Lock()
	defer t.admission.Unlock()
	ref, err := e.beginAdmitted(op, t)
	if err == nil {
		t.mu.Lock()
		if options.Isolation != DefaultIsolation {
			t.isolation = options.Isolation
		}
		t.readOnly = options.ReadOnly
		t.syncCharacteristics()
		t.mu.Unlock()
	}
	return ref, err
}

func (t *transactionOwner) validate(ref TxRef) error {
	if ref.owner != t.session.Engine() || ref.session != t.session.token || ref.id == 0 || ref.attachment != t {
		return errors.New("stuntgres: foreign or missing transaction reference")
	}
	if t.state == TxIdle || ref != t.ref {
		return errors.New("stuntgres: stale transaction reference")
	}
	return nil
}

func (e *Engine) Rollback(op *Operation, ref TxRef) (EndResult, error) {
	if err := e.transactionAdmission(op); err != nil {
		return EndResult{}, err
	}
	t := op.session.transactions
	t.admission.Lock()
	defer t.admission.Unlock()
	return e.endAdmitted(op, t, ref, false, false, false)
}

func (e *Engine) Commit(op *Operation, ref TxRef) (EndResult, error) {
	admissionErr := e.transactionAdmission(op)
	if admissionErr != nil && !errors.Is(admissionErr, context.Canceled) && !errors.Is(admissionErr, context.DeadlineExceeded) {
		return EndResult{}, admissionErr
	}
	t := op.session.transactions
	t.admission.Lock()
	defer t.admission.Unlock()
	return e.endAdmitted(op, t, ref, true, false, false)
}

// StatementScope serializes one session's statement, from parsing through Finish.
// Binding uses current resolution metadata; data consumers explicitly acquire a
// policy-selected committed view. Writes stage effects here, never in storage.
type StatementScope struct {
	self               *StatementScope
	owner              *transactionOwner
	op                 *Operation
	stage              WriteStage
	view               *VisibilityView
	finished           bool
	result             error
	worked             bool
	completed          bool
	controlOutcome     controlOutcome
	replacementRead    bool
	ticket             *relationTicket
	lockedRef          ObjectRef
	lockedDescriptor   Descriptor
	querySources       []queryLeaf
	queryLocks         *queryLockContext
	queryLockMode      relationLockMode
	lockedStorage      *relationStorage
	lockedIdentityOnly bool
	txRef              TxRef
	command            CommandID
	ownWrites          map[ObjectAddress][]rowVersion
	ownDeletes         map[ObjectAddress]*privateDeletionRoot
	Expected           *StatementDescription
	implicit           bool
	input              sqltype.InputEnvironment
	output             sqltype.OutputEnvironment
	sequences          *sequenceChanges
	searchPathResults  [2]sqltype.Value
	commentCatalog     CatalogRead
	commentRoles       RoleRead
}

type WriteStage struct {
	self      *WriteStage
	scope     *StatementScope
	rows      map[ObjectAddress][]rowVersion
	deletes   map[ObjectAddress]*privateDeletionRoot
	replacing bool
}

func (e *Engine) StartStatement(op *Operation) (*StatementScope, error) {
	if err := e.transactionAdmission(op); err != nil {
		return nil, err
	}
	t := op.session.transactions
	t.admission.Lock()
	if err := e.transactionAdmission(op); err != nil {
		t.admission.Unlock()
		return nil, err
	}
	s := &StatementScope{owner: t, op: op}
	s.self = s
	s.stage.scope = s
	s.stage.self = &s.stage
	// Capture presentation before engine locking; admission serializes settings
	// changes on this session for the lifetime of the statement.
	settings := op.session.settings
	settings.mu.Lock()
	if settings.value("bytea_output") == "escape" {
		s.output.ByteaOutput = sqltype.ByteaEscape
	}
	zone, ok := settings.typed["timezone"]
	if !ok {
		zone = settings.defaultTyped["timezone"]
	}
	style, ok := settings.typed["datestyle"]
	if !ok {
		style = settings.defaultTyped["datestyle"]
	}
	s.input.Zone, s.output.Zone = zone.zone, zone.zone
	s.input.DateStyle, s.output.DateStyle = style.dateStyle, style.dateStyle
	s.output.IntervalStyle = sqltype.ParseIntervalStyle(settings.value("intervalstyle"))
	s.input.IntervalStyle = s.output.IntervalStyle
	settings.mu.Unlock()
	return s, nil
}

func (s *StatementScope) active() error {
	if s == nil || s.self != s || s.finished {
		return errors.New("stuntgres: inactive statement scope")
	}
	return s.op.session.Engine().validateOperation(s.op)
}

func (s *StatementScope) gate(stmt parser.Statement) error {
	if err := s.active(); err != nil {
		return err
	}
	if stmt == nil {
		return nil
	}
	s.worked = true
	if control, ok := stmt.(*parser.SavepointControl); ok {
		return s.gateSavepoint(control)
	}
	if control, ok := stmt.(*parser.TransactionControl); ok {
		s.owner.mu.Lock()
		defer s.owner.mu.Unlock()
		if err := s.op.session.Engine().endPreflight(s.op); err != nil {
			return err
		}
		if control.Kind == parser.KindStart && s.owner.state == TxAborted {
			return sqlError("25P02", "current transaction is aborted, commands ignored until end of transaction block")
		}
		return nil
	}
	t := s.owner
	e := s.op.session.Engine()
	// Admission serializes this session, so the idle classification stays valid
	// while reserving replacement admission outside transaction synchronization.
	t.mu.Lock()
	idle := t.state == TxIdle
	t.mu.Unlock()
	if idle {
		switch stmt.(type) {
		case *parser.Insert, *parser.Update, *parser.Select, *parser.Delete:
			e.replacementMu.RLock()
			s.replacementRead = true
		case *parser.Comment:
			c := stmt.(*parser.Comment)
			if c.Kind == parser.CommentTable || c.Kind == parser.CommentColumn {
				e.replacementMu.RLock()
				s.replacementRead = true
			}
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == TxIdle {
		if err := e.beginLocked(s.op, t, TxImplicit); err != nil {
			return err
		}
	}
	if t.ticket == nil && t.state != TxAborted {
		ref := t.ref
		t.mu.Unlock()
		ticket, err := e.locks.register(ref)
		t.mu.Lock()
		if err != nil {
			return err
		}
		t.ticket = ticket
	}
	s.ticket = t.ticket
	s.txRef = t.ref
	s.implicit = t.state == TxImplicit
	s.input.TransactionStart = t.start
	s.command = t.command
	s.ownWrites = t.writes
	s.ownDeletes = t.deletes
	e.mu.Lock()
	stale := t.ref.epoch != e.state.epoch
	e.mu.Unlock()
	if stale {
		return ErrCatalogReplaced
	}
	if t.state == TxAborted {
		return sqlError("25P02", "current transaction is aborted, commands ignored until end of transaction block")
	}
	if t.command == CommandID(^uint64(0)) {
		return errors.New("stuntgres: command identity exhausted")
	}
	return nil
}

func (s *StatementScope) ResolutionCatalog() CatalogRead {
	if err := s.active(); err != nil {
		panic(err)
	}
	return s.op.session.Engine().CatalogRead()
}

func (s *StatementScope) Writes() *WriteStage { return &s.stage }

// Apply takes immutable payload ownership only after provenance and capacity
// checks. The binder supplied the original ObjectRef, not a reconstructed name.
func (w *WriteStage) Apply(ref ObjectRef, rows [][]sqltype.Value) error {
	if w == nil || w.self != w || w.scope == nil {
		return errors.New("stuntgres: foreign or missing write stage")
	}
	s := w.scope
	if err := s.active(); err != nil {
		return err
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if !s.worked || s.owner.state == TxIdle || s.owner.state == TxAborted {
		return errors.New("stuntgres: inactive write stage")
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.owner != e.owner {
		return errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.state.epoch || ref.Epoch != s.owner.ref.epoch {
		return ErrCatalogReplaced
	}
	if e.storage[ref.Address] == nil {
		return errors.New("stuntgres: relation storage is missing")
	}
	relation, ok := e.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok {
		return errors.New("stuntgres: invalid relation descriptor")
	}
	for _, row := range rows {
		if len(row) != len(relation.columns) {
			return errors.New("stuntgres: invalid staged row width")
		}
	}
	if len(rows) == 0 {
		return nil
	}
	n := uint64(len(rows))
	if n > ^uint64(0)-e.nextRow {
		return errors.New("stuntgres: row identity exhausted")
	}
	if n > ^uint64(0)-e.nextVersion {
		return errors.New("stuntgres: version identity exhausted")
	}
	versions := make([]rowVersion, len(rows))
	for i, row := range rows {
		e.nextRow++
		e.nextVersion++
		versions[i] = rowVersion{row: RowID(e.nextRow), version: VersionID(e.nextVersion), values: row}
	}
	if w.rows == nil {
		w.rows = make(map[ObjectAddress][]rowVersion)
	}
	if len(w.rows[ref.Address]) == 0 {
		w.rows[ref.Address] = versions
	} else {
		w.rows[ref.Address] = append(w.rows[ref.Address], versions...)
	}
	return nil
}

func (s *StatementScope) Describe(stmt parser.Statement) (StatementDescription, error) {
	if err := s.gate(stmt); err != nil {
		return StatementDescription{}, err
	}
	if _, ok := stmt.(*parser.SavepointControl); ok {
		s.controlOutcome = controlDescribed
		return StatementDescription{}, nil
	}
	if _, ok := stmt.(*parser.TransactionControl); ok {
		if err := validateModes(stmt.(*parser.TransactionControl).Modes); err != nil {
			return StatementDescription{}, err
		}
		s.controlOutcome = controlDescribed
		return StatementDescription{}, nil
	}
	if characteristics, ok := stmt.(*parser.TransactionCharacteristics); ok {
		return StatementDescription{}, validateModes(characteristics.Modes)
	}
	if err := s.snapshotPhase(stmt); err != nil {
		return StatementDescription{}, err
	}
	if s.owner.state == TxActive && explicitUtility(stmt) {
		return StatementDescription{}, sqlError("0A000", "statement is unsupported in an explicit transaction")
	}
	if err := s.acquireRelation(stmt, false); err != nil {
		return StatementDescription{}, err
	}
	return s.op.session.Engine().describeOperation(s, stmt)
}

func (s *StatementScope) Execute(stmt parser.Statement, count int, importFrame FrameImporter) (Result, error) {
	// Adapter argument normalization precedes scope admission.
	if err := s.gate(stmt); err != nil {
		return Result{}, err
	}
	if control, ok := stmt.(*parser.SavepointControl); ok {
		return s.executeSavepoint(control, count)
	}
	if control, ok := stmt.(*parser.TransactionControl); ok {
		return s.executeControl(control, count)
	}
	if characteristics, ok := stmt.(*parser.TransactionCharacteristics); ok {
		return s.executeCharacteristics(characteristics, count)
	}
	if err := s.snapshotPhase(stmt); err != nil {
		return Result{}, err
	}
	if _, ok := stmt.(*parser.LockTable); ok && count != 0 {
		return Result{}, errors.New("stuntgres: LOCK TABLE takes no arguments")
	}
	// Utility read-only restrictions precede object resolution, ACL checks and
	// locks. DML keeps its separate analysis-before-read-only execution gate.
	if err := s.readOnlyUtilityEligibility(stmt); err != nil {
		return Result{}, err
	}
	if err := s.acquireRelation(stmt, true); err != nil {
		return Result{}, err
	}
	if _, ok := stmt.(*parser.LockTable); ok {
		return Result{}, nil
	}
	if err := s.executionEligibility(stmt); err != nil {
		return Result{}, err
	}
	return s.op.session.Engine().executeStatement(s, stmt, count, importFrame)
}

// Prepare only vector headers/capacity. Tail writes follow the final preflight.
func prepareOwnWrites(previous, staged map[ObjectAddress][]rowVersion, replacing bool) map[ObjectAddress][]rowVersion {
	if len(staged) == 0 {
		return previous
	}
	next := make(map[ObjectAddress][]rowVersion, len(previous)+len(staged))
	for a, rows := range previous {
		next[a] = rows
	}
	for a, rows := range staged {
		old := previous[a]
		if replacing {
			next[a] = mergePrivateRows(old, rows, true)
			continue
		}
		if len(old) == 0 {
			next[a] = rows
			continue
		}
		size := len(old) + len(rows)
		if size <= cap(old) {
			next[a] = old[:size]
			continue
		}
		capacity := max(size, cap(old)*2)
		dst := make([]rowVersion, size, capacity)
		copy(dst, old)
		next[a] = dst
	}
	return next
}

func (s *StatementScope) Finish(err error) error {
	if s == nil || s.self != s {
		return errors.New("stuntgres: foreign or missing statement scope")
	}
	if s.finished {
		return s.result
	}
	if s.controlOutcome == controlExecuted || (s.controlOutcome == controlDescribed && err == nil) {
		// A control has no stage/view. In particular, do not advance or recheck
		// the new incarnation installed by CHAIN after its linearization.
		s.finished = true
		s.result = nil
		s.owner.admission.Unlock()
		return nil
	}
	t := s.owner
	e := s.op.session.Engine()
	t.mu.Lock()
	// Parsing can fail before classification; admitted failures still abort
	// explicit work. Completed row/catalog/session utilities have already passed
	// their installation preflight; later cancellation cannot retract success.
	if err == nil && s.worked && !s.completed {
		err = e.validateOperation(s.op)
	}
	if err == nil && s.worked {
		if s.sequences != nil {
			t.sequences = s.sequences
		}
		if t.state == TxImplicit {
			if !s.completed {
				err = e.publishWrites(s.op, t.ref, s.stage.rows, s.stage.deletes)
			}
		} else {
			deletes := mergeDeletions(t.deletes, s.stage.deletes)
			writes := prepareOwnWrites(t.writes, s.stage.rows, s.stage.replacing)
			session := s.op.session
			e.mu.Lock()
			session.mu.Lock()
			err = publicationOperationLocked(e, s.op)
			if err == nil && t.ref.epoch != e.state.epoch {
				err = ErrCatalogReplaced
			}
			if err == nil {
				for a, rows := range s.stage.rows {
					if !s.stage.replacing && len(t.writes[a]) != 0 {
						copy(writes[a][len(t.writes[a]):], rows)
					}
				}
				t.writes = writes
				t.deletes = deletes
			}
			session.mu.Unlock()
			e.mu.Unlock()
		}
		if err == nil {
			t.command++
		}
	}
	var diagnostic *sqlstate.Error
	admittedFailure := s.worked || errors.As(err, &diagnostic) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	var endedTicket *relationTicket
	if t.state == TxImplicit {
		t.session.settings.finishTransaction(err == nil)
		endedTicket = t.ticket
		t.ticket = nil
		t.clear()
	} else if err != nil && admittedFailure && (t.state == TxActive || t.state == TxAborted) {
		if errors.Is(err, ErrCatalogReplaced) {
			t.truncateSavepoints(0)
		}
		if len(t.savepoints) != 0 {
			if t.state == TxActive {
				mark := t.savepoints[len(t.savepoints)-1]
				t.restoreSavepoint(mark, mark.settings.clone())
			}
			t.state = TxAborted
		} else {
			t.session.settings.finishTransaction(false)
			t.isolation, t.readOnly = t.entryIsolation, t.entryReadOnly
			t.syncCharacteristics()
			t.state = TxAborted
			t.writes = nil
			t.deletes = nil
			t.roots = nil
			t.frontier = 0
			t.sequences = nil
			endedTicket = t.ticket
			t.ticket = nil
		}
	}
	if s.view != nil {
		s.view.Release()
		s.view = nil
	}
	s.stage.rows = nil
	s.stage.deletes = nil
	s.ownDeletes = nil
	s.ownWrites = nil
	s.ticket = nil
	s.lockedDescriptor = nil
	s.lockedStorage = nil
	s.querySources = nil
	s.queryLocks = nil
	s.queryLockMode = 0
	s.lockedIdentityOnly = false
	s.finished = true
	s.result = err
	t.mu.Unlock()
	if s.replacementRead {
		e.replacementMu.RUnlock()
		s.replacementRead = false
	}
	e.locks.end(endedTicket)
	t.admission.Unlock()
	return err
}

// Caller holds Session.mu; row/catalog publication additionally holds Engine.mu.
func publicationOperationLocked(e *Engine, op *Operation) error {
	if op == nil || op.self != op || op.session == nil || op.session.token.owner != e {
		return errors.New("stuntgres: foreign or missing operation")
	}
	if op.done || op.session.state != live {
		return errors.New("stuntgres: inactive operation")
	}
	if _, ok := op.session.operations[op]; !ok {
		return errors.New("stuntgres: inactive operation")
	}
	return op.ctx.Err()
}
