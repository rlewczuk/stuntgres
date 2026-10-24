package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type EndResult struct{ Committed bool }

func clearTransaction(t *session.TransactionOwner) {
	ownerExtensionOf(t).retireForeign()
	if payloadOf(t.Session).cursors != nil {
		ownerExtensionOf(t).detachCursors(payloadOf(t.Session).cursors.endTransaction(t.Ref))
	}
	if payloadOf(t.Session).preparedSQL != nil {
		for _, p := range payloadOf(t.Session).preparedSQL.portals {
			if p.tx == t.Ref {
				p.retired = true
			}
		}
	}
	ownerExtensionOf(t).exclusionModes = index.ExclusionModes{}
	t.Clear()
}

// transactionSessionEnd is the owner seen as its session's Transaction
// resource. It is a pointer conversion of the owner, so attaching it
// allocates nothing.
type transactionSessionEnd session.TransactionOwner

func (r *transactionSessionEnd) EndSession(token ids.SessionToken, _ session.EndReason) error {
	return endTransactionSession((*session.TransactionOwner)(r), token)
}

// endTransactionSession aborts t's transaction and releases its locks,
// cursors and TEMP session state when the session ends.
func endTransactionSession(t *session.TransactionOwner, token ids.SessionToken) error {
	if err := t.AcquireAdmission(context.Background(), nil); err != nil {
		return err
	}
	defer t.Admission.Unlock()
	t.Mu.Lock()
	if token != t.Session.SessionToken {
		t.Mu.Unlock()
		return errors.New("stuntgres: foreign transaction session")
	}
	engineOf(t.Session).endSerializable(t.Ref, false)
	t.Session.Settings.FinishTransaction(false)
	clearTransaction(t)
	t.Session.RetireTransactionTimeout()
	ticket := t.Ticket
	t.Ticket = nil
	t.Mu.Unlock()
	engineOf(t.Session).endLockOwner(ticket)
	cleanupErr := drainCursorCleanup(t, context.Background(), nil)
	// Collation-bearing namespaces are retained for lawful runner teardown after
	// this callback releases admission. Other TEMP cleanup retains its owner path.
	if err := engineOf(t.Session).endTemporarySession(t.Session); cleanupErr == nil {
		cleanupErr = err
	}
	return cleanupErr
}

func (e *Engine) beginLocked(op *session.Operation, t *session.TransactionOwner, state session.TxState, start time.Time) error {
	if t.Next == ids.TxID(^uint64(0)) {
		return errors.New("stuntgres: transaction identity exhausted")
	}
	sess := op.Session
	e.mu.Lock()
	defer e.mu.Unlock()
	settings := sess.Settings
	settings.Mu.Lock()
	defer settings.Mu.Unlock()
	sess.Mu.Lock()
	defer sess.Mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return err
	}
	diagnostic, err := e.visibility.NextDiagnosticTxLocked()
	if err != nil {
		return err
	}
	t.Next++
	t.Ref = e.catalog.Domain.Tx(t.Session.SessionToken, t.Next, e.catalog.state.Epoch(), t, diagnostic)
	payloadOf(sess).notifications.Busy = true
	t.BeginLocked(settings, state, start)
	e.enrollSerializableLocked(t.Ref, t.Isolation)
	sess.BeginTimeoutLocked(t.Ref, state == session.TxActive, start)
	return nil
}

func (e *Engine) Begin(op *session.Operation, options session.TxOptions) (ids.TxRef, error) {
	start := time.Now().UTC().Truncate(time.Microsecond)
	if options.Isolation > session.Serializable {
		return ids.TxRef{}, errors.New("stuntgres: unsupported transaction isolation level")
	}
	if err := e.transactionAdmission(op); err != nil {
		return ids.TxRef{}, err
	}
	t := op.Session.Transactions
	if err := t.AcquireAdmission(op.Context(), op); err != nil {
		return ids.TxRef{}, err
	}
	defer t.Admission.Unlock()
	ref, err := e.beginAdmitted(op, t, start)
	if err == nil {
		t.Mu.Lock()
		if options.Isolation != session.DefaultIsolation {
			t.Isolation = options.Isolation
		}
		t.ReadOnly = options.ReadOnly
		if options.DeferrableSet {
			t.Deferrable = options.Deferrable
		}
		syncCharacteristics(t)
		t.Mu.Unlock()
	}
	return ref, err
}

func (e *Engine) Rollback(op *session.Operation, ref ids.TxRef) (EndResult, error) {
	if err := e.transactionAdmission(op); err != nil {
		return EndResult{}, err
	}
	t := op.Session.Transactions
	if err := t.AcquireAdmission(op.Context(), op); err != nil {
		return EndResult{}, err
	}
	defer t.Admission.Unlock()
	result, err := e.endAdmitted(op, t, ref, false, false, false, time.Time{}, nil)
	if cleanupErr := drainCursorCleanup(t, op.Context(), nil); err == nil {
		err = cleanupErr
	}
	return result, err
}

func (e *Engine) Commit(op *session.Operation, ref ids.TxRef) (EndResult, error) {
	admissionErr := e.transactionAdmission(op)
	if admissionErr != nil && !errors.Is(admissionErr, context.Canceled) && !errors.Is(admissionErr, context.DeadlineExceeded) {
		return EndResult{}, admissionErr
	}
	t := op.Session.Transactions
	if err := t.AcquireAdmission(op.Context(), op); err != nil {
		return EndResult{}, err
	}
	defer t.Admission.Unlock()
	result, err := e.endAdmitted(op, t, ref, true, false, false, time.Time{}, nil)
	if cleanupErr := drainCursorCleanup(t, op.Context(), nil); err == nil {
		err = cleanupErr
	}
	return result, err
}

// StatementScope serializes one session's statement, from parsing through Finish.
// Binding uses current resolution metadata; data consumers explicitly acquire a
// policy-selected committed view. Writes stage effects here, never in storage.
type StatementScope struct {
	statementMutation
	currentSnapshot         *sqltype.Snapshot
	snapshotFrontier        ids.CommitSeq
	snapshotTarget          visibility.Root
	snapshotTargets         map[ids.ObjectAddress]visibility.Root
	snapshotTargetAdmission bool // Actual snapshotPhase command, not CTE bookkeeping.
	mutations               []statementMutation
	activeMutation          int
	cteInvocation           *exec.CTEInvocation
	self                    *StatementScope
	owner                   *session.TransactionOwner
	op                      *session.Operation
	ruleAction              ruleActionScope
	rowSecurity             bool                      // Immutable effective setting for this admitted statement.
	rlsActive               []ids.ObjectAddress       // Active policy expansion path, not an occurrence cache.
	rlsPrograms             []*rowSecurityProgram     // Owns runtime plus original selected admission identities.
	rlsAdmissionDepth       int                       // Whole target/source admission, including recursive policies.
	rlsCommand              *preparedCommandExecution // Active original logical command, lexical scratch.
	rlsImportWaits          uint64                    // Scope-monotonic actual policy-source waits, never row work.
	rlsDataRefs             []ids.ObjectRef           // Complete prepared physical union before the first snapshot.
	authority               *exec.Authority
	// Borrowed only during this statement's admission/execution. Admission
	// refreshes this coherent snapshot serially before publishing parallel reads;
	// it is neither prepared-plan state nor a cached permission decision.
	routineAuth                 AuthorizationView
	stage                       WriteStage
	notificationActions         []session.NotificationAction
	notificationEvents          []session.Notification
	view                        *visibility.View
	finished                    bool
	message                     bool
	messageEnded                bool
	messageResult               error
	result                      error
	worked                      bool
	completed                   bool
	sessionCompleted            bool // Successful session utilities have no private stage to merge.
	controlOutcome              controlOutcome
	replacementRead             bool
	ticket                      *lock.Ticket
	multiEvents                 []lock.MultiEvent
	querySources                []exec.Leaf
	queryLockAdmission          analyze.LockAdmission
	queryLocks                  *exec.LockContext
	txRef                       ids.TxRef
	command                     ids.CommandID
	ownWrites                   map[ids.ObjectAddress][]storage.RowVersion
	ownDeletes                  map[ids.ObjectAddress]*visibility.PrivateDeletes
	Expected                    *StatementDescription
	admissionWaited             bool
	implicit                    bool
	input                       sqltype.InputEnvironment
	routinePrograms             []commandRoutineProgram // Consumed command graphs; same admission lease as exec.Authority.
	statementStart              time.Time
	transactionStart            time.Time
	lease                       session.StatementLease // identity for session-layer state; never copied on purpose
	output                      sqltype.OutputEnvironment
	initialParameters           []sqltype.Usage
	activeSQLRoutineDepth       int                         // Root invocation owner; never saved/reset as lexical scratch.
	effectCommand, effectSerial uint64                      // admission-private command event ownership; never rewound
	quoteCaptures               *exec.QuoteCaptureCandidate // sealed before executor/source publication
	catalog                     *session.Journal
	largeObjectRead             catalog.Read // Immutable active-query READ view, saved/restored by SQL body activation.
	catalogAuthority            *catalogAuthority
	projectedRoots              bool
	foreignActionAuth           *AuthorizationView
	identity                    exec.IdentitySnapshot
	// Feature state lives in slots. Groups that every statement writes are
	// values. A pointer slot xSlot stays nil until its first write through
	// x(); reads use xOrZero(), which never allocates, so nil means all zero.
	// Only the SQL routine scratch swaps slot pointers (protocol,
	// presentation); nothing else resets a slot to nil.
	inquiry            inquirySnapshot
	prepared           preparedRebind
	sqlRoutine         sqlRoutineDemand
	inheritance        inheritanceScope
	domains            domainScope
	protocolSlot       *protocolScope
	presentationSlot   *presentationCaches
	truncateSlot       *dml.TruncateScope
	dropsSlot          *dropScope
	foreignActionsSlot *dml.ForeignActionScope
	foreignKeysSlot    *table.ForeignKeyCreation
	viewCreationSlot   *view.CreationScope
	materializedSlot   *view.MaterializedRefreshScope
	indexesSlot        *index.Scope
	partitionsSlot     *partition.Scope
	foreignTablesSlot  *foreignTableScope
}

type WriteStage struct {
	self      *WriteStage
	scope     *StatementScope
	rows      map[ids.ObjectAddress][]storage.RowVersion
	deletes   map[ids.ObjectAddress]*visibility.PrivateDeletes
	replacing bool
}

func (e *Engine) StartStatement(op *session.Operation) (*StatementScope, error) {
	start := op.StatementStart()
	if err := e.transactionAdmission(op); err != nil {
		return nil, e.finishStatementAdmissionError(op, err, false)
	}
	t := op.Session.Transactions
	if err := t.AcquireAdmission(op.Context(), op); err != nil {
		return nil, e.finishStatementAdmissionError(op, err, false)
	}
	if err := e.transactionAdmission(op); err != nil {
		return nil, e.finishStatementAdmissionError(op, err, true)
	}
	return e.newStatementScope(op, t, start), nil
}

// Held-cursor precommit borrows a fresh current operation/presentation scope
// without starting a new admitted statement or provisional timeout.
func newStatementScope(op *session.Operation, owner *session.TransactionOwner, start time.Time) *StatementScope {
	s := &StatementScope{owner: owner, op: op, statementStart: start}
	s.self = s
	s.lease.Statement = s
	s.stage.scope = s
	s.stage.self = &s.stage
	s.input, s.output = op.Session.Settings.Presentation()
	// Only private Restore INSERT replay owns this authority. The same input
	// flows to literal analysis and execution-time coercions; ordinary scopes
	// (including statements after Restore) retain normal BOX normalization.
	s.input.RawBoxReplay = operationReplayOrZero(op).insertAuthority
	settings := op.Session.Settings
	settings.Mu.Lock()
	s.rowSecurity = settings.Value("row_security") == "on"
	settings.Mu.Unlock()
	return s
}

func (s *StatementScope) active() error {
	if s == nil || s.self != s || s.finished {
		return errors.New("stuntgres: inactive statement scope")
	}
	return s.engine().validateOperation(s.op)
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
		s.owner.Mu.Lock()
		defer s.owner.Mu.Unlock()
		if s.message {
			if err := s.admitTransactionLocked(s.engine()); err != nil {
				return err
			}
		}
		if err := s.engine().endPreflight(s.op); err != nil {
			return err
		}
		if (control.Kind == parser.KindStart || control.Kind == parser.KindCommitPrepared || control.Kind == parser.KindRollbackPrepared) && s.owner.State == session.TxAborted {
			return sqlError("25P02", "current transaction is aborted, commands ignored until end of transaction block")
		}
		return nil
	}
	t := s.owner
	e := s.engine()
	// Admission serializes this session, so the idle classification stays valid
	// while reserving replacement admission outside transaction synchronization.
	t.Mu.Lock()
	idle := t.State == session.TxIdle
	t.Mu.Unlock()
	if idle {
		switch stmt.(type) {
		case *parser.Copy, *parser.Insert, *parser.Update, *parser.Select, *parser.Delete, *parser.Merge, *parser.Discard, *parser.Truncate:
			e.replacementMu.RLock()
			s.replacementRead = true
		case *parser.Comment:
			c := stmt.(*parser.Comment)
			if c.Kind == parser.CommentTable || c.Kind == parser.CommentForeignTable || c.Kind == parser.CommentColumn {
				e.replacementMu.RLock()
				s.replacementRead = true
			}
		}
	}
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if err := s.admitTransactionLocked(e); err != nil {
		return err
	}
	e.mu.Lock()
	stale := t.Ref.Epoch() != e.catalog.state.Epoch()
	e.mu.Unlock()
	if stale {
		return ids.ErrCatalogReplaced
	}
	if t.State == session.TxAborted {
		return sqlError("25P02", "current transaction is aborted, commands ignored until end of transaction block")
	}
	if t.Command == ids.CommandID(^uint64(0)) {
		return errors.New("stuntgres: command identity exhausted")
	}
	return nil
}

// The transaction owner mutex is held; admission protects the projected roots.
func (s *StatementScope) attachTransactionLocked() {
	t := s.owner
	s.ticket = t.Ticket
	s.txRef = t.Ref
	s.implicit = t.State == session.TxImplicit
	s.input.TransactionStart = t.Start
	s.command = t.Command
	s.ownWrites = t.Writes
	s.ownDeletes = t.Deletes
}

func (s *StatementScope) ResolutionCatalog() catalog.Read {
	if err := s.active(); err != nil {
		panic(err)
	}
	return s.engine().CatalogRead()
}

func (s *StatementScope) Writes() *WriteStage { return &s.stage }

// Apply takes immutable payload ownership only after provenance and capacity
// checks. The binder supplied the original ObjectRef, not a reconstructed name.
func (w *WriteStage) Apply(ref ids.ObjectRef, rows [][]sqltype.Value) error {
	if w == nil || w.self != w || w.scope == nil {
		return errors.New("stuntgres: foreign or missing write stage")
	}
	s := w.scope
	if err := s.active(); err != nil {
		return err
	}
	batch := index.WriteBatch{Values: rows}
	for i := range batch.Values {
		candidate := batch.Candidate(i)
		if err := dml.CheckUniqueRow(s.writeHost(), ref, &candidate, nil, batch.Prefix(i)); err != nil {
			return err
		}
		batch.Set(i, candidate)
	}
	return dml.ApplyValidated(s.writeHost(), ref, batch)
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
	case *parser.Do:
		return s.engine().describeOperation(s, stmt)
	case *parser.Listen, *parser.Unlisten, *parser.Notify:
		return StatementDescription{}, nil
	case *parser.AlterTableAllInTablespace:
		return s.engine().describeOperation(s, stmt)
	case *parser.ForeignDDL:
		return s.engine().describeOperation(s, stmt)
	case *parser.CreateDatabaseStmt, *parser.CreateTablespace, *parser.Load, *parser.Vacuum:
		return s.engine().describeOperation(s, stmt)
	case *parser.Truncate:
		return s.engine().describeOperation(s, stmt)
	case *parser.RefreshMaterializedView:
		return s.engine().describeOperation(s, stmt)
	case *parser.FetchCursor, *parser.CloseCursor, *parser.PrepareSQL, *parser.ExecuteSQL, *parser.DeallocateSQL, *parser.Discard:
		return s.engine().describeOperation(s, stmt)
	case *parser.AlterDomain, *parser.DropDomain:
		// This narrow utility has no parameter/result signature. Preparation
		// must remain quiet; Execute owns read-only-first and explicit-block
		// rejection, without resolving or changing a domain during analysis.
		return s.engine().describeOperation(s, stmt)
	case *parser.AlterOwner:
		if stmt.(*parser.AlterOwner).Kind == "domain" {
			return s.engine().describeOperation(s, stmt)
		}
	case *parser.CreateAccessMethod, *parser.DropAccessMethod:
		return s.engine().describeOperation(s, stmt)
	case *parser.CreateCast, *parser.DropCast, *parser.CreateFunction, *parser.CreateLanguage, *parser.CreateAggregate, *parser.DropRoutine, *parser.AlterRoutine, *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator, *parser.CreateOperatorFamily, *parser.AlterOperatorFamily, *parser.DropOperatorFamily, *parser.CreateOperatorClass, *parser.AlterOperatorClass, *parser.DropOperatorClass, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		// These utilities have no result signature. Admission, read-only
		// precedence and the explicit-transaction boundary belong to Execute.
		return s.engine().describeOperation(s, stmt)
	case *parser.ObjectGrant:
		// Analysis is effect-free even inside an explicit transaction. Execute
		// retains read-only-first rejection and the non-database utility boundary.
		return s.engine().describeOperation(s, stmt)
	}
	if transactionalSchemaUtility(stmt) {
		if _, queryCreation := stmt.(*parser.CreateQueryTable); !queryCreation {
			return s.engine().describeOperation(s, stmt)
		}
	}
	_, queryCreation := stmt.(*parser.CreateQueryTable)
	if queryCreation {
		if err := s.executionEligibility(stmt); err != nil {
			return StatementDescription{}, err
		}
	}
	if err := s.snapshotAnalysisPhase(stmt); err != nil {
		return StatementDescription{}, err
	}
	switch stmt.(type) {
	case *parser.CreateTrigger, *parser.AlterTrigger, *parser.DropTrigger, *parser.CreateRule, *parser.AlterRule, *parser.DropRule:
		return s.engine().describeOperation(s, stmt)
	case *parser.CreateTable, *parser.DropTable, *parser.CreateView, *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy, *parser.AlterTableOf, *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition, *parser.AlterComposite, *parser.AlterType, *parser.DropType:
		// Definition utilities have no result signature. Resolve ownership, read-only
		// precedence and persistent-DDL exclusions only when actually executing.
		return s.engine().describeOperation(s, stmt)
	}
	indexBuild := session.EarlyIndexUtility(stmt)
	if !queryCreation && s.owner.State == session.TxActive && session.ExplicitUtility(stmt) && !indexBuild {
		return StatementDescription{}, sqlError("0A000", "statement is unsupported in an explicit transaction")
	}
	if err := s.selectPreparedAnalysis(stmt); err != nil {
		return StatementDescription{}, err
	}
	if err := s.acquireRelation(stmt, false); err != nil {
		return StatementDescription{}, err
	}
	return s.engine().describeOperation(s, stmt)
}

func (s *StatementScope) Execute(stmt parser.Statement, count int, importFrame FrameImporter) (result Result, err error) {
	return s.execute(stmt, count, importFrame, nil)
}

// One canonical resolver/importer/executor serves embedded and protocol callers.
func (s *StatementScope) execute(stmt parser.Statement, count int, importFrame FrameImporter, stream *protocolCopyExecution) (result Result, err error) {
	var before session.TxState
	if s != nil && s.owner != nil {
		s.owner.Mu.Lock()
		before = s.owner.State
		s.owner.Mu.Unlock()
	}
	// Adapter argument normalization precedes scope admission.
	execute, wrapped := stmt.(*parser.ExecuteSQL)
	preparedInvocation := false
	if wrapped && s != nil {
		expected := s.Expected
		defer func() { s.Expected = expected }()
		preparedInvocation = s.protocolOrZero().command && expected != nil && importFrame != nil && expected.Parameters.Count == count
	}
	var utilityFrame []sqltype.Value
	for execute != nil {
		if err := s.active(); err != nil {
			return Result{}, err
		}
		if count != 0 && !preparedInvocation {
			return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
		}
		if count != 0 || stream != nil && s.protocolOrZero().simple {
			// SQL EXECUTE is an outer utility: its portal exists before the
			// prepared-name lookup, actual arguments and inner planning.
			if err := s.gate(stmt); err != nil {
				return Result{}, err
			}
			if stream != nil && s.protocolOrZero().simple {
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
		entry := payloadOf(s.op.Session).preparedSQL.entries[execute.Name.Text]
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
	base, counted, insert, ok := session.ClassifyCommandTag(stmt, before)
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
	if s.protocolOrZero().simpleStreaming && !wrapped && session.ProtocolPlainSelect(stmt) {
		p := &protocolPortal{entry: sqlPreparedStatement{body: stmt}, tx: s.txRef}
		s.protocol().simplePortal, s.protocol().bind = p, p
		if count == 0 && importFrame == nil {
			importFrame = func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
				return nil
			}
		}
	}
	if !session.ProtocolPlannedStatement(stmt) {
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
	// VACUUM owns option-first admission and never takes a data snapshot.
	if _, ok := stmt.(*parser.Vacuum); ok {
		return s.engine().executeStatement(s, stmt, count, importFrame)
	}
	if _, ok := stmt.(*parser.CreateDatabaseStmt); ok {
		// Cluster creation has no local data snapshot. Reject read-only mode
		// before explicit or retained-message blocks, then resolve options and
		// authority only in the executor.
		if err := s.readOnlyUtilityEligibility(stmt); err != nil {
			return Result{}, err
		}
		if err := s.executionEligibility(stmt); err != nil {
			return Result{}, err
		}
		return s.engine().executeStatement(s, stmt, count, importFrame)
	}
	_, queryCreation := stmt.(*parser.CreateQueryTable)
	if queryCreation && !transactionalSchemaUtility(stmt) {
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
	if err := s.snapshotExecutionAdmission(stmt); err != nil {
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
		return s.engine().executeStatement(s, stmt, count, importFrame)
	}
	indexBuild := session.EarlyIndexUtility(stmt)
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
	// SELECT (including cursor and query COPY sources) acquires its RC pair
	// in final source admission. Other commands need it before any expression.
	switch stmt.(type) {
	case *parser.Select, *parser.DeclareCursor:
	default:
		if err := s.snapshotPhase(stmt); err != nil {
			return Result{}, err
		}
	}
	if _, ok := stmt.(*parser.LockTable); ok {
		return Result{}, nil
	}
	if !earlyEligibility {
		if err := s.executionEligibility(stmt); err != nil {
			return Result{}, err
		}
	}
	if !queryCreation && session.ModifyingTag(stmt) != "" {
		index, concurrent := stmt.(*parser.CreateIndex)
		if !concurrent || index.Concurrent.Character == 0 {
			e := s.engine()
			e.mu.Lock()
			err := e.visibilityWriteLocked(s.txRef)
			e.mu.Unlock()
			if err != nil {
				return Result{}, err
			}
		}
	}
	return s.engine().executeStatement(s, stmt, count, importFrame)
}

func drainCursorCleanup(t *session.TransactionOwner, ctx context.Context, scope *StatementScope) error {
	t.Mu.Lock()
	x := ownerExtensionOf(t)
	cursors, participants := x.cursorCleanup, x.foreign.TakeCleanup()
	x.cursorCleanup = nil
	t.Mu.Unlock()
	wire := scope != nil && (scope.protocolOrZero().command || scope.protocolOrZero().simple)
	var err error
	if wire && len(cursors) != 0 {
		err = scope.protocolTerminalIO(func() error { return disposeSQLCursors(ctx, cursors) })
	} else {
		err = disposeSQLCursors(ctx, cursors)
	}
	if failure := fdw.Rollback(ctx, participants); err == nil {
		err = failure
	}
	if payloadOf(t.Session).preparedSQL == nil {
		return err
	}
	for name, p := range payloadOf(t.Session).preparedSQL.portals {
		if p.retired {
			delete(payloadOf(t.Session).preparedSQL.portals, name)
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

// scopeOfLease returns the statement scope that holds l, or nil.
func scopeOfLease(l *session.StatementLease) *StatementScope {
	if l == nil {
		return nil
	}
	s, _ := l.Statement.(*StatementScope)
	return s
}
