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
	Isolation     Isolation
	ReadOnly      bool
	Deferrable    bool
	DeferrableSet bool
}

type TxRef struct {
	owner      *Engine
	session    SessionToken
	id         TxID
	epoch      CatalogEpoch
	attachment *transactionOwner
	diagnostic uint32
}

func (r TxRef) ID() TxID { return r.id }

type EndResult struct{ Committed bool }

type rowVersion struct {
	row     RowID
	version VersionID
	born    CommitSeq
	// The last committed key transition survives key restoration and payload
	// replacements. Private successors accumulate transitions until commit.
	keyChangedAt     CommitSeq
	pendingKeyChange bool
	// base is the committed predecessor for a private replacement; zero is an insert.
	base   VersionID
	values []sqltype.Value
	// Private admission captures index readiness; committed rows clear this pin.
	indexAdmission *catalogState
}

type transactionOwner struct {
	admission       sync.Mutex
	ticket          *relationTicket
	mu              sync.Mutex
	session         *Session
	state           TxState
	ref             TxRef
	next            TxID
	command         CommandID
	writes          map[ObjectAddress][]rowVersion
	deletes         map[ObjectAddress]*privateDeletionRoot
	start           time.Time
	isolation       Isolation
	readOnly        bool
	deferrable      bool
	entryIsolation  Isolation
	entryReadOnly   bool
	entryDeferrable bool
	snapshotSet     bool
	frontier        CommitSeq
	roots           map[ObjectAddress]transactionRoot
	// Retained with the existing snapshot; consumed only by pg_sequence.
	sequenceCatalog   *catalogState
	catalog           *catalogChanges
	savepoints        []savepointMark
	accessedTemporary bool
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
	t.sequenceCatalog = nil
	t.catalog = nil
	clear(t.savepoints)
	t.savepoints = nil
	t.accessedTemporary = false
}

func (t *transactionOwner) EndSession(token SessionToken, _ EndReason) error {
	t.admission.Lock()
	defer t.admission.Unlock()
	t.mu.Lock()
	if token != t.session.token {
		t.mu.Unlock()
		return errors.New("stuntgres: foreign transaction session")
	}
	t.session.Engine().endSerializable(t.ref, false)
	t.session.settings.finishTransaction(false)
	t.clear()
	ticket := t.ticket
	t.ticket = nil
	t.mu.Unlock()
	t.session.Engine().locks.end(ticket)
	t.session.Engine().endTemporarySession(t.session)
	return nil
}

func (s *Session) TransactionState() TxState {
	t := s.transactions
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

func (e *Engine) beginLocked(op *Operation, t *transactionOwner, state TxState, start time.Time) error {
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
	if e.diagnosticTx == ^uint32(0) {
		return errors.New("stuntgres: transaction diagnostic identity exhausted")
	}
	e.diagnosticTx++
	t.next++
	t.ref = TxRef{owner: e, session: t.session.token, id: t.next, epoch: e.state.epoch, attachment: t, diagnostic: e.diagnosticTx}
	t.state = state
	t.isolation = isolationFromText(settings.value("default_transaction_isolation"))
	t.readOnly = settings.value("default_transaction_read_only") == "on"
	t.deferrable = settings.value("default_transaction_deferrable") == "on"
	t.entryIsolation, t.entryReadOnly = t.isolation, t.readOnly
	t.entryDeferrable = t.deferrable
	settings.currentIsolation = isolationText(t.isolation)
	settings.currentReadOnly = t.readOnly
	settings.currentDeferrable = t.deferrable
	e.enrollSerializableLocked(t.ref, t.isolation)
	t.start = start
	return nil
}

func (e *Engine) Begin(op *Operation, options TxOptions) (TxRef, error) {
	start := time.Now().UTC().Truncate(time.Microsecond)
	if options.Isolation > Serializable {
		return TxRef{}, errors.New("stuntgres: unsupported transaction isolation level")
	}
	if err := e.transactionAdmission(op); err != nil {
		return TxRef{}, err
	}
	t := op.session.transactions
	t.admission.Lock()
	defer t.admission.Unlock()
	ref, err := e.beginAdmitted(op, t, start)
	if err == nil {
		t.mu.Lock()
		if options.Isolation != DefaultIsolation {
			t.isolation = options.Isolation
		}
		t.readOnly = options.ReadOnly
		if options.DeferrableSet {
			t.deferrable = options.Deferrable
		}
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
	return e.endAdmitted(op, t, ref, false, false, false, time.Time{})
}

func (e *Engine) Commit(op *Operation, ref TxRef) (EndResult, error) {
	admissionErr := e.transactionAdmission(op)
	if admissionErr != nil && !errors.Is(admissionErr, context.Canceled) && !errors.Is(admissionErr, context.DeadlineExceeded) {
		return EndResult{}, admissionErr
	}
	t := op.session.transactions
	t.admission.Lock()
	defer t.admission.Unlock()
	return e.endAdmitted(op, t, ref, true, false, false, time.Time{})
}

// StatementScope serializes one session's statement, from parsing through Finish.
// Binding uses current resolution metadata; data consumers explicitly acquire a
// policy-selected committed view. Writes stage effects here, never in storage.
type StatementScope struct {
	self                   *StatementScope
	owner                  *transactionOwner
	op                     *Operation
	stage                  WriteStage
	view                   *VisibilityView
	finished               bool
	result                 error
	worked                 bool
	completed              bool
	controlOutcome         controlOutcome
	replacementRead        bool
	ticket                 *relationTicket
	lockedRef              ObjectRef
	viewCreationNamespace  ObjectRef
	lockedDescriptor       Descriptor
	indexMaintenance       *indexMaintenanceGrant
	querySources           []queryLeaf
	queryLockAdmission     queryLockAdmission
	queryLocks             *queryLockContext
	activeViews            []ObjectAddress
	lockedStorage          *relationStorage
	lockedIdentityOnly     bool
	txRef                  TxRef
	command                CommandID
	ownWrites              map[ObjectAddress][]rowVersion
	ownDeletes             map[ObjectAddress]*privateDeletionRoot
	uniqueBinding          *uniqueWriteBinding
	Expected               *StatementDescription
	implicit               bool
	input                  sqltype.InputEnvironment
	statementStart         time.Time
	output                 sqltype.OutputEnvironment
	catalog                *catalogChanges
	catalogAuthority       *catalogAuthority
	projectedRoots         bool
	searchPathResults      [2]sqltype.Value
	commentCatalog         CatalogRead
	commentRoles           RoleRead
	deparseAuth            AuthorizationView
	visibilityCatalog      CatalogRead
	visibilityDemand       bool
	visibilityRelationPath []ObjectAddress
	visibilityOtherPath    []ObjectAddress
	identity               identitySnapshot
	currentSchemaResult    sqltype.Value
	deparsePath            []ObjectAddress
}

type WriteStage struct {
	self      *WriteStage
	scope     *StatementScope
	rows      map[ObjectAddress][]rowVersion
	deletes   map[ObjectAddress]*privateDeletionRoot
	replacing bool
}

func (e *Engine) StartStatement(op *Operation) (*StatementScope, error) {
	var start time.Time
	if op != nil && !op.commandStart.IsZero() {
		start = op.commandStart
	} else {
		start = time.Now().UTC().Truncate(time.Microsecond)
	}
	if err := e.transactionAdmission(op); err != nil {
		return nil, err
	}
	t := op.session.transactions
	t.admission.Lock()
	if err := e.transactionAdmission(op); err != nil {
		t.admission.Unlock()
		return nil, err
	}
	s := &StatementScope{owner: t, op: op, statementStart: start}
	s.self = s
	s.stage.scope = s
	s.stage.self = &s.stage
	// Capture presentation before engine locking; admission serializes settings
	// changes on this session for the lifetime of the statement.
	s.input, s.output = sessionPresentation(op.session)
	return s, nil
}

// Admission serializes session settings; publication uses the same typed
// presentation capture when formatting its supplementary integrity fence.
func sessionPresentation(session *Session) (input sqltype.InputEnvironment, output sqltype.OutputEnvironment) {
	settings := session.settings
	settings.mu.Lock()
	defer settings.mu.Unlock()
	if settings.value("bytea_output") == "escape" {
		output.ByteaOutput = sqltype.ByteaEscape
	}
	zone, ok := settings.typed["timezone"]
	if !ok {
		zone = settings.defaultTyped["timezone"]
	}
	style, ok := settings.typed["datestyle"]
	if !ok {
		style = settings.defaultTyped["datestyle"]
	}
	abbreviations, ok := settings.typed["timezone_abbreviations"]
	if !ok {
		abbreviations = settings.defaultTyped["timezone_abbreviations"]
	}
	input.TimezoneAbbreviations = abbreviations.timezoneAbbreviations
	input.Zone, output.Zone = zone.zone, zone.zone
	input.DateStyle, output.DateStyle = style.dateStyle, style.dateStyle
	output.IntervalStyle = sqltype.ParseIntervalStyle(settings.value("intervalstyle"))
	input.IntervalStyle = output.IntervalStyle
	output.ExtraFloatDigits = int32(settings.typedValue("extra_float_digits").number)
	output.FloatPrecisionSet = true
	input.NumericLocale = settings.typedValue("lc_numeric").locale
	input.MonetaryLocale = settings.typedValue("lc_monetary").locale
	input.TimeLocale = settings.typedValue("lc_time").locale
	output.NumericLocale, output.MonetaryLocale, output.TimeLocale = input.NumericLocale, input.MonetaryLocale, input.TimeLocale
	return input, output
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
		if (control.Kind == parser.KindStart || control.Kind == parser.KindCommitPrepared || control.Kind == parser.KindRollbackPrepared) && s.owner.state == TxAborted {
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
		case *parser.Insert, *parser.Update, *parser.Select, *parser.Delete, *parser.Merge:
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
		if err := e.beginLocked(s.op, t, TxImplicit, s.statementStart); err != nil {
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
	for i, row := range rows {
		if err := s.checkUniqueRow(ref, row, nil, rows[:i]); err != nil {
			return err
		}
	}
	return w.applyValidated(ref, rows)
}

// SQL producers validate each accepted prefix before demanding the next row;
// only this canonical staging method transfers the completed immutable batch.
func (w *WriteStage) applyValidated(ref ObjectRef, rows [][]sqltype.Value) error {
	s := w.scope
	if err := s.active(); err != nil {
		return err
	}
retry:
	s.owner.mu.Lock()
	e := s.op.session.Engine()
	e.mu.Lock()
	admission := e.privateCatalogLocked(s.owner.catalog).state
	if binding := s.uniqueBinding; binding != nil && binding.catalog != admission {
		e.mu.Unlock()
		s.owner.mu.Unlock()
		for i, row := range rows {
			if err := s.checkUniqueRow(ref, row, nil, rows[:i]); err != nil {
				return err
			}
		}
		goto retry
	}
	defer s.owner.mu.Unlock()
	defer e.mu.Unlock()
	if !s.worked || s.owner.state == TxIdle || s.owner.state == TxAborted {
		return errors.New("stuntgres: inactive write stage")
	}
	if ref.owner != e.owner {
		return errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.state.epoch || ref.Epoch != s.owner.ref.epoch {
		return ErrCatalogReplaced
	}
	if e.privateStorageLocked(s.owner.catalog, ref.Address) == nil {
		return errors.New("stuntgres: relation storage is missing")
	}
	relation, ok := e.privateCatalogLocked(s.owner.catalog).state.descriptors[ref.Address].(*relationDescriptor)
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
	versions, err := e.allocateRowVersionsLocked(rows, admission)
	if err != nil {
		return err
	}
	if serializableRelevant(e.serializable.nodes[s.txRef]) {
		for _, row := range versions {
			event := [1]serializableWrite{{row: row.row, version: row.version}}
			if err := e.serializableWriteLocked(s, ref, event[:]); err != nil {
				return err
			}
		}
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
		s.controlOutcome = controlDescribed
		return StatementDescription{}, nil
	}
	if _, ok := stmt.(*parser.TransactionCharacteristics); ok {
		return StatementDescription{}, nil
	}
	_, queryCreation := stmt.(*parser.CreateQueryTable)
	if queryCreation {
		if err := s.executionEligibility(stmt); err != nil {
			return StatementDescription{}, err
		}
	}
	if err := s.snapshotPhase(stmt); err != nil {
		return StatementDescription{}, err
	}
	switch stmt.(type) {
	case *parser.CreateTable, *parser.DropTable:
		// Table utilities have no result signature. Resolve ownership, read-only
		// precedence and persistent-DDL exclusions only when actually executing.
		return s.op.session.Engine().describeOperation(s, stmt)
	}
	indexBuild := earlyIndexUtility(stmt)
	if !queryCreation && s.owner.state == TxActive && explicitUtility(stmt) && !indexBuild {
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
	_, queryCreation := stmt.(*parser.CreateQueryTable)
	if queryCreation {
		// Rejected creation utilities must not enter a deferrable safe-snapshot wait.
		if err := s.readOnlyUtilityEligibility(stmt); err != nil {
			return Result{}, err
		}
		if err := s.executionEligibility(stmt); err != nil {
			return Result{}, err
		}
	}
	if err := s.snapshotPhase(stmt); err != nil {
		return Result{}, err
	}
	if _, ok := stmt.(*parser.LockTable); ok && count != 0 {
		return Result{}, errors.New("stuntgres: LOCK TABLE takes no arguments")
	}
	// Utility read-only restrictions precede object resolution, ACL checks and
	// locks. DML keeps its separate analysis-before-read-only execution gate.
	if !queryCreation {
		if err := s.readOnlyUtilityEligibility(stmt); err != nil {
			return Result{}, err
		}
	}
	indexBuild := earlyIndexUtility(stmt)
	earlyEligibility := indexBuild || queryCreation
	if earlyEligibility && !queryCreation {
		if err := s.executionEligibility(stmt); err != nil {
			return Result{}, err
		}
	}
	if err := s.acquireRelation(stmt, true); err != nil {
		return Result{}, err
	}
	if _, ok := stmt.(*parser.LockTable); ok {
		return Result{}, nil
	}
	if !earlyEligibility {
		if err := s.executionEligibility(stmt); err != nil {
			return Result{}, err
		}
	}
	if modifyingTag(stmt) != "" {
		index, concurrent := stmt.(*parser.CreateIndex)
		if !concurrent || index.Concurrent.Character == 0 {
			e := s.op.session.Engine()
			e.mu.Lock()
			err := e.visibilityWriteLocked(s.txRef)
			e.mu.Unlock()
			if err != nil {
				return Result{}, err
			}
		}
	}
	return s.op.session.Engine().executeStatement(s, stmt, count, importFrame)
}

// Both ordinary writes and initial catalog data reserve globally nonreused
// identities. Engine.mu protects both checked counters; failed work may leave gaps.
func (e *Engine) allocateRowVersionsLocked(rows [][]sqltype.Value, admission *catalogState) ([]rowVersion, error) {
	n := uint64(len(rows))
	if n > ^uint64(0)-e.nextRow {
		return nil, errors.New("stuntgres: row identity exhausted")
	}
	if n > ^uint64(0)-e.nextVersion {
		return nil, errors.New("stuntgres: version identity exhausted")
	}
	versions := make([]rowVersion, len(rows))
	for i, row := range rows {
		e.nextRow++
		e.nextVersion++
		versions[i] = rowVersion{row: RowID(e.nextRow), version: VersionID(e.nextVersion), values: row, indexAdmission: admission}
	}
	return versions, nil
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
		s.result = err
		s.owner.admission.Unlock()
		return err
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
		catalog := t.catalog
		if s.catalog != nil {
			catalog = s.catalog
		}
		if t.state == TxImplicit {
			if !s.completed {
				err = e.publishRows(s.op, t.ref, s.stage.rows, s.stage.deletes, catalog, s.catalogAuthority)
			}
		} else {
			var deletes map[ObjectAddress]*privateDeletionRoot
			var writes map[ObjectAddress][]rowVersion
			if s.projectedRoots {
				writes, deletes = s.stage.rows, s.stage.deletes
			} else {
				deletes = mergeDeletions(t.deletes, s.stage.deletes)
			}
			err = s.validatePrivateIndexExpressions(catalog, deletes)
			if err == nil {
				if !s.projectedRoots {
					writes = prepareOwnWrites(t.writes, s.stage.rows, s.stage.replacing)
				}
				session := s.op.session
				e.mu.Lock()
				session.mu.Lock()
				err = publicationOperationLocked(e, s.op)
				if err == nil && t.ref.epoch != e.state.epoch {
					err = ErrCatalogReplaced
				}
				if err == nil && s.catalog != nil {
					err = e.catalogPreflightLocked(catalog)
				}
				if err == nil {
					err = e.catalogAuthorityPreflightLocked(s.catalogAuthority)
				}
				if err == nil {
					for a, rows := range s.stage.rows {
						if !s.stage.replacing && len(t.writes[a]) != 0 {
							copy(writes[a][len(t.writes[a]):], rows)
						}
					}
					t.writes = writes
					t.deletes = deletes
					t.catalog = catalog
					e.reconcileSerializableWritesLocked(t)
				}
				session.mu.Unlock()
				e.mu.Unlock()
			}
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
		e.endSerializable(t.ref, err == nil)
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
				e.locks.restore(t.ticket, mark.locks)
			}
			t.state = TxAborted
		} else {
			t.session.settings.finishTransaction(false)
			e.endSerializable(t.ref, false)
			t.isolation, t.readOnly = t.entryIsolation, t.entryReadOnly
			t.deferrable = t.entryDeferrable
			t.state = TxAborted
			t.syncCharacteristics()
			t.writes = nil
			t.deletes = nil
			t.roots = nil
			t.sequenceCatalog = nil
			t.frontier = 0
			t.catalog = nil
			endedTicket = t.ticket
			t.ticket = nil
		}
	}
	e.endStatementVisibility(s.txRef, t.state != TxIdle && stableIsolation(t.isolation))
	if s.view != nil {
		s.view.Release()
		s.view = nil
	}
	s.stage.rows = nil
	s.stage.deletes = nil
	s.ownDeletes = nil
	s.ownWrites = nil
	s.uniqueBinding = nil
	s.ticket = nil
	s.lockedDescriptor = nil
	s.indexMaintenance = nil
	s.viewCreationNamespace = ObjectRef{}
	s.lockedStorage = nil
	s.querySources = nil
	s.queryLockAdmission = queryLockAdmission{}
	s.queryLocks = nil
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
