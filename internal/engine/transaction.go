package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"sync"
	"time"
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
	domain     *Domain
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
	indexOutputs   map[ObjectAddress]indexRowOutput // Private, immutable produced index data.
}

type transactionOwner struct {
	admission       sync.Mutex
	copyScope       *StatementScope
	copyDone        chan struct{}
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
	// Retained with the existing snapshot; consumed by pg_sequence and pg_sequences.
	sequenceCatalog     *catalogState
	catalog             *catalogChanges
	savepoints          []savepointMark
	accessedTemporary   bool
	exclusionModes      exclusionModes
	retainedMessage     bool           // Admission spans a direct multi-command message.
	protocolCycle       *ProtocolCycle // Retention only; never owns admission.
	identityEntry       authorization.RoleState
	identityCaptured    bool
	messageBorn         bool
	cursorCleanup       []*sqlCursor // Detached under owner lock; drained unlocked.
	notificationActions []notificationAction
	notificationEvents  []Notification
}

func (t *transactionOwner) clear() {
	if t.session.cursors != nil {
		t.cursorCleanup = append(t.cursorCleanup, t.session.cursors.endTransaction(t.ref)...)
	}
	if t.session.preparedSQL != nil {
		for _, p := range t.session.preparedSQL.portals {
			if p.tx == t.ref {
				p.retired = true
			}
		}
	}
	if t.protocolCycle != nil {
		t.protocolCycle.executed = false
	}
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
	t.notificationActions = nil
	t.notificationEvents = nil
	clear(t.savepoints)
	t.savepoints = nil
	t.accessedTemporary = false
	t.exclusionModes = exclusionModes{}
}

func (t *transactionOwner) EndSession(token SessionToken, _ EndReason) error {
	if err := t.acquireAdmission(context.Background(), nil); err != nil {
		return err
	}
	defer t.admission.Unlock()
	t.mu.Lock()
	if token != t.session.token {
		t.mu.Unlock()
		return errors.New("stuntgres: foreign transaction session")
	}
	t.session.Engine().endSerializable(t.ref, false)
	t.session.settings.finishTransaction(false)
	t.clear()
	t.session.retireTransactionTimeout()
	ticket := t.ticket
	t.ticket = nil
	t.mu.Unlock()
	t.session.Engine().endLockOwner(ticket)
	cleanupErr := t.drainCursorCleanup(context.Background(), nil)
	// Collation-bearing namespaces are retained for lawful runner teardown after
	// this callback releases admission. Other TEMP cleanup retains its owner path.
	if err := t.session.Engine().endTemporarySession(t.session); cleanupErr == nil {
		cleanupErr = err
	}
	return cleanupErr
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
	t.ref = TxRef{domain: e.domain, session: t.session.token, id: t.next, epoch: e.state.epoch, attachment: t, diagnostic: e.diagnosticTx}
	t.state = state
	session.notifications.busy = true
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
	if t.retainedMessage {
		t.messageBorn = true
	}
	session.beginTimeoutLocked(t.ref, state == TxActive, start)
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
	if err := t.acquireAdmission(op.Context(), op); err != nil {
		return TxRef{}, err
	}
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
	if ref.domain != domainOf(t.session.Engine()) || ref.session != t.session.token || ref.id == 0 || ref.attachment != t {
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
	if err := t.acquireAdmission(op.Context(), op); err != nil {
		return EndResult{}, err
	}
	defer t.admission.Unlock()
	result, err := e.endAdmitted(op, t, ref, false, false, false, time.Time{}, nil)
	if cleanupErr := t.drainCursorCleanup(op.Context(), nil); err == nil {
		err = cleanupErr
	}
	return result, err
}

func (e *Engine) Commit(op *Operation, ref TxRef) (EndResult, error) {
	admissionErr := e.transactionAdmission(op)
	if admissionErr != nil && !errors.Is(admissionErr, context.Canceled) && !errors.Is(admissionErr, context.DeadlineExceeded) {
		return EndResult{}, admissionErr
	}
	t := op.session.transactions
	if err := t.acquireAdmission(op.Context(), op); err != nil {
		return EndResult{}, err
	}
	defer t.admission.Unlock()
	result, err := e.endAdmitted(op, t, ref, true, false, false, time.Time{}, nil)
	if cleanupErr := t.drainCursorCleanup(op.Context(), nil); err == nil {
		err = cleanupErr
	}
	return result, err
}

// StatementScope serializes one session's statement, from parsing through Finish.
// Binding uses current resolution metadata; data consumers explicitly acquire a
// policy-selected committed view. Writes stage effects here, never in storage.
type StatementScope struct {
	statementMutation
	mutations      []statementMutation
	activeMutation int
	cteInvocation  *cteInvocation
	self           *StatementScope
	owner          *transactionOwner
	op             *Operation
	ruleActionAuth *AuthorizationView
	authority      *queryAuthority
	// Borrowed only during this statement's admission/execution. Admission
	// refreshes this coherent snapshot serially before publishing parallel reads;
	// it is neither prepared-plan state nor a cached permission decision.
	routineAuth                    AuthorizationView
	stage                          WriteStage
	notificationActions            []notificationAction
	notificationEvents             []Notification
	view                           *VisibilityView
	partitionValidationView        *VisibilityView // Latest DDL scan, same owner; never the retained query snapshot.
	finished                       bool
	message                        bool
	messageEnded                   bool
	messageResult                  error
	result                         error
	worked                         bool
	completed                      bool
	sessionCompleted               bool // Successful session utilities have no private stage to merge.
	controlOutcome                 controlOutcome
	replacementRead                bool
	ticket                         *relationTicket
	multiEvents                    []multiEvent
	viewCreationNamespace          ObjectRef
	viewPersistence                string
	viewBinding                    binding
	viewChanges                    *catalogChanges
	indexMaintenance               *indexMaintenanceGrant
	materializedRefresh            bool
	materializedSources            []Descriptor
	materializedSourceView         *visibilityData
	querySources                   []queryLeaf
	foreignOwner                   *foreignScanOwner
	foreignCursor                  *sqlCursor
	queryLockAdmission             queryLockAdmission
	queryLocks                     *queryLockContext
	activeViews                    []ObjectAddress
	txRef                          TxRef
	command                        CommandID
	ownWrites                      map[ObjectAddress][]rowVersion
	ownDeletes                     map[ObjectAddress]*privateDeletionRoot
	existingIndexOutputs           map[ObjectAddress]map[indexRowIdentity]indexRowOutput
	exclusionModes                 exclusionModes
	exclusionStarted               bool
	inheritanceRelations           []queryLeaf // Exact physical SELECT grants, separate from SQL occurrences.
	inheritanceCatalog             CatalogRead // Immutable topology after the last grant/wait reconciliation.
	inheritanceAlter               *inheritanceAlterState
	consumedColumnDrops            map[*preparedDrop]struct{}
	truncateRelations              []truncateRelation
	truncateSequences              []truncateSequence
	truncateRestart                bool
	truncateNames                  []truncateName
	truncateSelected               map[ObjectAddress]int
	Expected                       *StatementDescription
	preparedReuse                  *preparedAnalysis
	preparedInvalid                bool
	preparedPath                   sequenceEnvironment
	preparedNamespace              uint64
	preparedPrivate                *catalogChanges
	preparedEpoch                  CatalogEpoch
	preparedChangedDuringAdmission bool
	admissionWaited                bool
	implicit                       bool
	input                          sqltype.InputEnvironment
	domainEvaluation               *EvalContext
	domainPreparedChecks           map[*typeDescriptor]domainCheckProgram
	partitionPrograms              map[partitionRoutineIdentity][]BoundExpr
	routinePrograms                []commandRoutineProgram // Consumed command graphs; same admission lease as queryAuthority.
	domainInputCatalog             CatalogRead
	statementStart                 time.Time
	transactionStart               time.Time
	timeoutRef                     TxRef // admission projection for preparse ERROR timer retirement
	output                         sqltype.OutputEnvironment
	initialParameters              []sqltype.Usage
	protocolBind                   *protocolPortal
	protocolResume                 bool
	protocolSimple                 bool
	protocolSimpleCreated          bool
	protocolSimpleStreaming        bool
	protocolSimplePortal           *protocolPortal
	protocolCommand                bool // Explicit wire preparation/execution policy, not session state.
	// Statement execution has a single evaluator owner; this cache never belongs
	// to a held plan and is discarded with the statement scope.
	pgRegex                     sqltype.PGRegexExecution
	quoteCaptures               *quoteCaptureCandidate // sealed before executor/source publication
	catalog                     *catalogChanges
	catalogAuthority            *catalogAuthority
	projectedRoots              bool
	foreignKeyTargets           []ObjectRef
	alterForeignKeys            []alterForeignKeyAdmission
	foreignActionAdmissions     map[ObjectAddress]*foreignActionAdmission
	foreignActionTransitions    []foreignActionTransition
	foreignActionsSettling      bool
	foreignActionExclusionAdded bool // a new event admitted while settling actions
	foreignActionAuth           *AuthorizationView
	foreignActionJobs           []foreignKeyJob
	searchPathResults           [2]sqltype.Value
	commentCatalog              CatalogRead
	commentRoles                authorization.Read
	deparseAuth                 AuthorizationView
	visibilityCatalog           CatalogRead
	visibilityDemand            bool
	visibilityRelationPath      []ObjectAddress
	visibilityOtherPath         []ObjectAddress
	conversionDefaults          map[[2]int]conversionDefault
	regprocOutput               map[uint32]string
	regprocedureOutput          map[uint32]string
	identity                    identitySnapshot
	currentSchemaResult         sqltype.Value
	deparsePath                 []ObjectAddress
	operatorOutput              map[operatorOutputKey]string
	operatorGroups              operatorGroups
	collationPath               []ObjectAddress
	regTSOutput                 map[regTSOutputKey]string
}

type WriteStage struct {
	self      *WriteStage
	scope     *StatementScope
	rows      map[ObjectAddress][]rowVersion
	deletes   map[ObjectAddress]*privateDeletionRoot
	replacing bool
}

func (e *Engine) StartStatement(op *Operation) (*StatementScope, error) {
	start := statementOperationStart(op)
	if err := e.transactionAdmission(op); err != nil {
		return nil, err
	}
	t := op.session.transactions
	if err := t.acquireAdmission(op.Context(), op); err != nil {
		return nil, err
	}
	if err := e.transactionAdmission(op); err != nil {
		t.admission.Unlock()
		return nil, err
	}
	return e.newStatementScope(op, t, start), nil
}

// Held-cursor precommit borrows a fresh current operation/presentation scope
// without starting a new admitted statement or provisional timeout.
func newStatementScope(op *Operation, owner *transactionOwner, start time.Time) *StatementScope {
	s := &StatementScope{owner: owner, op: op, statementStart: start}
	s.self = s
	s.stage.scope = s
	s.stage.self = &s.stage
	s.input, s.output = sessionPresentation(op.session)
	return s
}

func statementOperationStart(op *Operation) time.Time {
	if op != nil && !op.commandStart.IsZero() {
		return op.commandStart
	}
	return time.Now().UTC().Truncate(time.Microsecond)
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
	output.QuoteAllIdentifiers = settings.value("quote_all_identifiers") == "on"
	_, output.ClientEncoding = sqltype.ClientEncodingName(settings.value("client_encoding"))
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
		if s.message {
			if err := s.admitTransactionLocked(s.op.session.Engine()); err != nil {
				return err
			}
		}
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
		case *parser.Copy, *parser.Insert, *parser.Update, *parser.Select, *parser.Delete, *parser.Merge, *parser.Discard, *parser.Truncate:
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
	if err := s.admitTransactionLocked(e); err != nil {
		return err
	}
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

// The transaction owner mutex is held; admission protects the projected roots.
func (s *StatementScope) attachTransactionLocked() {
	t := s.owner
	s.ticket = t.ticket
	s.txRef = t.ref
	s.implicit = t.state == TxImplicit
	s.input.TransactionStart = t.start
	s.command = t.command
	s.ownWrites = t.writes
	s.ownDeletes = t.deletes
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
	batch := indexWriteBatch{values: rows}
	for i := range batch.values {
		candidate := batch.candidate(i)
		if err := s.checkUniqueRow(ref, &candidate, nil, batch.prefix(i)); err != nil {
			return err
		}
		batch.set(i, candidate)
	}
	return w.applyValidated(ref, batch)
}

// SQL producers validate each accepted prefix before demanding the next row;
// only this canonical staging method transfers the completed immutable batch.
func (w *WriteStage) applyValidated(ref ObjectRef, batch indexWriteBatch) error {
	rows := batch.values
	s := w.scope
	if err := s.active(); err != nil {
		return err
	}
retry:
	if err := s.validateIndexCandidates(ref, &batch); err != nil {
		return err
	}
	s.owner.mu.Lock()
	e := s.op.session.Engine()
	e.mu.Lock()
	admission := e.privateCatalogLocked(s.owner.catalog).state
	if binding := s.uniqueBinding; binding != nil && binding.catalog != admission {
		e.mu.Unlock()
		s.owner.mu.Unlock()
		if s.insertConflict != nil && s.insertConflict.update == nil {
			var err error
			batch, err = s.revalidateConflictRows(ref, batch)
			rows = batch.values
			if err != nil {
				return err
			}
		} else {
			if err := s.revalidateIndexRows(ref, nil, &batch); err != nil {
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
	if ref.owner != e.domain {
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
	if s.insertConflict != nil && s.insertConflict.update == nil {
		s.insertConflict.affected = len(rows)
	}
	if len(rows) == 0 {
		return nil
	}
	if err := e.assignTransactionXIDLocked(s.txRef); err != nil {
		return err
	}
	var versions []rowVersion
	var err error
	if len(relation.exclusionConstraints) != 0 {
		versions = make([]rowVersion, len(rows))
		for i := range rows {
			reserved := batch.candidate(i).exclusion
			if reserved == nil {
				return errors.New("stuntgres: exclusion row lost its admission identity")
			}
			versions[i] = *reserved
			versions[i].indexAdmission = admission
		}
	} else {
		versions, err = e.allocateRowVersionsLocked(rows, admission)
	}
	if err != nil {
		return err
	}
	if batch.states != nil {
		for i := range versions {
			versions[i].indexOutputs = batch.states[i].indexes
		}
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
	if copy, ok := stmt.(*parser.Copy); ok {
		return StatementDescription{}, CopyBoundary(copy)
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
	if _, ok := stmt.(*parser.SetConstraints); ok {
		return StatementDescription{}, nil
	}
	if _, ok := stmt.(*parser.AlterTableRenameConstraint); ok {
		return StatementDescription{}, nil
	}
	switch stmt.(type) {
	case *parser.Listen, *parser.Unlisten, *parser.Notify:
		return StatementDescription{}, nil
	case *parser.AlterTableAllInTablespace:
		return s.op.session.Engine().describeOperation(s, stmt)
	case *parser.ForeignDDL:
		return s.op.session.Engine().describeOperation(s, stmt)
	case *parser.CreateTablespace:
		return s.op.session.Engine().describeOperation(s, stmt)
	case *parser.Truncate:
		return s.op.session.Engine().describeOperation(s, stmt)
	case *parser.RefreshMaterializedView:
		return s.op.session.Engine().describeOperation(s, stmt)
	case *parser.FetchCursor, *parser.CloseCursor, *parser.PrepareSQL, *parser.ExecuteSQL, *parser.DeallocateSQL, *parser.Discard:
		return s.op.session.Engine().describeOperation(s, stmt)
	case *parser.AlterDomain:
		// This narrow utility has no parameter/result signature. Preparation
		// must remain quiet; Execute owns read-only-first and explicit-block
		// rejection, without resolving or changing a domain during analysis.
		return s.op.session.Engine().describeOperation(s, stmt)
	case *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		// These utilities have no result signature. Admission, read-only
		// precedence and the explicit-transaction boundary belong to Execute.
		return s.op.session.Engine().describeOperation(s, stmt)
	case *parser.ObjectGrant:
		// Analysis is effect-free even inside an explicit transaction. Execute
		// retains read-only-first rejection and the non-database utility boundary.
		return s.op.session.Engine().describeOperation(s, stmt)
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
	case *parser.CreateRule, *parser.AlterRule, *parser.DropRule:
		return s.op.session.Engine().describeOperation(s, stmt)
	case *parser.CreateTable, *parser.DropTable, *parser.CreateView, *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy, *parser.AlterTableOf, *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition, *parser.AlterComposite, *parser.DropComposite:
		// Definition utilities have no result signature. Resolve ownership, read-only
		// precedence and persistent-DDL exclusions only when actually executing.
		return s.op.session.Engine().describeOperation(s, stmt)
	}
	indexBuild := earlyIndexUtility(stmt)
	if !queryCreation && s.owner.state == TxActive && explicitUtility(stmt) && !indexBuild {
		return StatementDescription{}, sqlError("0A000", "statement is unsupported in an explicit transaction")
	}
	if err := s.selectPreparedAnalysis(stmt); err != nil {
		return StatementDescription{}, err
	}
	if err := s.acquireRelation(stmt, false); err != nil {
		return StatementDescription{}, err
	}
	return s.op.session.Engine().describeOperation(s, stmt)
}

func (s *StatementScope) Execute(stmt parser.Statement, count int, importFrame FrameImporter) (result Result, err error) {
	return s.execute(stmt, count, importFrame, nil)
}

// One canonical resolver/importer/executor serves embedded and protocol callers.
func (s *StatementScope) execute(stmt parser.Statement, count int, importFrame FrameImporter, stream *protocolCopyExecution) (result Result, err error) {
	var before TxState
	if s != nil && s.owner != nil {
		s.owner.mu.Lock()
		before = s.owner.state
		s.owner.mu.Unlock()
	}
	// Adapter argument normalization precedes scope admission.
	execute, wrapped := stmt.(*parser.ExecuteSQL)
	preparedInvocation := false
	if wrapped && s != nil {
		expected := s.Expected
		defer func() { s.Expected = expected }()
		preparedInvocation = s.protocolCommand && expected != nil && importFrame != nil && expected.Parameters.Count == count
	}
	var utilityFrame []sqltype.Value
	for execute != nil {
		if err := s.active(); err != nil {
			return Result{}, err
		}
		if count != 0 && !preparedInvocation {
			return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
		}
		if count != 0 || stream != nil && s.protocolSimple {
			// SQL EXECUTE is an outer utility: its portal exists before the
			// prepared-name lookup, actual arguments and inner planning.
			if err := s.gate(stmt); err != nil {
				return Result{}, err
			}
			if stream != nil && s.protocolSimple {
				if err := s.beginProtocolSimplePortal(); err != nil {
					return Result{}, err
				}
			}
		}
		if count != 0 {
			// This invocation owns its utility formals. Evaluate/import them
			// once before the next lookup; nested utility $n remains unbound.
			formal := s.Expected.Parameters
			if cap(utilityFrame) < formal.Count {
				utilityFrame = make([]sqltype.Value, formal.Count)
			} else {
				utilityFrame = utilityFrame[:formal.Count]
			}
			importErr := importFrame(s.op.Context(), formal, utilityFrame, s.input)
			clear(utilityFrame)
			if importErr != nil {
				return Result{}, importErr
			}
		}
		entry := s.op.session.preparedSQL.entries[execute.Name.Text]
		if entry == nil {
			// Establish the ordinary failed-command transaction boundary too.
			if err := s.gate(stmt); err != nil {
				return Result{}, err
			}
			return Result{}, missingSQLPrepared(execute.Name.Text)
		}
		stmt = entry.body
		s.Expected = &entry.description
		count = entry.description.Parameters.Count
		_, copyBody := stmt.(*parser.Copy)
		nextExecute, executeBody := stmt.(*parser.ExecuteSQL)
		importFrame = s.sqlExecuteImporter(execute, executeBody || stream != nil && copyBody)
		preparedInvocation = true
		execute = nextExecute
	}
	base, counted, insert, ok := classifyCommandTag(stmt, before)
	if !ok {
		return Result{}, sqlError("0A000", "statement command tag is not supported")
	}
	defer func() {
		if err != nil || result.CommandTag != "" {
			return
		}
		if insert {
			result.CommandTag = base + " 0 " + strconv.FormatInt(result.Count, 10)
		} else if counted {
			result.CommandTag = base + " " + strconv.FormatInt(result.Count, 10)
		} else {
			result.CommandTag = base
		}
	}()
	if err := s.gate(stmt); err != nil {
		return Result{}, err
	}
	if s.protocolSimpleStreaming && !wrapped && protocolPlainSelect(stmt) {
		p := &protocolPortal{entry: sqlPreparedStatement{body: stmt}, tx: s.txRef}
		s.protocolSimplePortal, s.protocolBind = p, p
		if count == 0 && importFrame == nil {
			importFrame = func(context.Context, ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error { return nil }
		}
	}
	if !protocolPlannedStatement(stmt) {
		if err := s.beginProtocolSimplePortal(); err != nil {
			return Result{}, err
		}
	}
	if copy, ok := stmt.(*parser.Copy); ok {
		if stream == nil {
			return Result{}, CopyBoundary(copy)
		}
		if wrapped && count != 0 {
			formal := s.Expected.Parameters
			frame := make([]sqltype.Value, formal.Count)
			if err := importFrame(s.op.Context(), formal, frame, s.input); err != nil {
				return Result{}, err
			}
		}
		copied, copyErr := s.CopyProtocol(copy, stream.source, stream.destination, stream.hooks)
		return Result{Count: copied}, copyErr
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
	if constraints, ok := stmt.(*parser.SetConstraints); ok {
		return s.executeSetConstraints(constraints, count)
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
	if _, ok := stmt.(*parser.RefreshMaterializedView); ok {
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
	if _, ok := stmt.(*parser.Truncate); ok && count != 0 {
		return Result{}, errors.New("stuntgres: TRUNCATE TABLE takes no arguments")
	}
	// Utility read-only restrictions precede object resolution, ACL checks and
	// locks. DML keeps its separate analysis-before-read-only execution gate.
	if !queryCreation {
		if err := s.readOnlyUtilityEligibility(stmt); err != nil {
			return Result{}, err
		}
	}
	if _, ok := stmt.(*parser.CreateTablespace); ok {
		if err := s.executionEligibility(stmt); err != nil {
			return Result{}, err
		}
		return s.op.session.Engine().executeStatement(s, stmt, count, importFrame)
	}
	indexBuild := earlyIndexUtility(stmt)
	_, refreshing := stmt.(*parser.RefreshMaterializedView)
	earlyEligibility := indexBuild || queryCreation || refreshing
	if earlyEligibility && !queryCreation {
		if err := s.executionEligibility(stmt); err != nil {
			return Result{}, err
		}
	}
	if err := s.selectPreparedAnalysis(stmt); err != nil {
		return Result{}, err
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

func (t *transactionOwner) drainCursorCleanup(ctx context.Context, scope *StatementScope) error {
	t.mu.Lock()
	cursors := t.cursorCleanup
	t.cursorCleanup = nil
	t.mu.Unlock()
	wire := scope != nil && (scope.protocolCommand || scope.protocolSimple)
	var err error
	if wire && len(cursors) != 0 {
		err = scope.protocolTerminalIO(func() error { return disposeSQLCursors(ctx, cursors) })
	} else {
		err = disposeSQLCursors(ctx, cursors)
	}
	if t.session.preparedSQL == nil {
		return err
	}
	for name, p := range t.session.preparedSQL.portals {
		if p.retired {
			delete(t.session.preparedSQL.portals, name)
			var failure error
			if wire {
				failure = scope.protocolTerminalIO(func() error { return p.close(ctx) })
			} else {
				failure = p.close(ctx)
			}
			if err == nil {
				err = failure
			}
		}
	}
	return err
}
