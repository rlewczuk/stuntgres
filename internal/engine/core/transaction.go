package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type EndResult struct{ Committed bool }

func clearTransaction(t *session.TransactionOwner) {
	ownerExtensionOf(t).retireForeign()
	if PayloadOf(t.Session).Cursors != nil {
		ownerExtensionOf(t).detachCursors(PayloadOf(t.Session).Cursors.endTransaction(t.Ref))
	}
	if PayloadOf(t.Session).PreparedSQL != nil {
		for _, p := range PayloadOf(t.Session).PreparedSQL.Bindings() {
			if p.Transaction == t.Ref {
				p.Retired = true
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
	EngineOf(t.Session).endSerializable(t.Ref, false)
	t.Session.Settings.FinishTransaction(false)
	clearTransaction(t)
	t.Session.RetireTransactionTimeout()
	ticket := t.Ticket
	t.Ticket = nil
	t.Mu.Unlock()
	EngineOf(t.Session).EndLockOwner(ticket)
	cleanupErr := drainCursorCleanup(t, context.Background(), nil)
	// Collation-bearing namespaces are retained for lawful runner teardown after
	// this callback releases admission. Other TEMP cleanup retains its owner path.
	if err := EngineOf(t.Session).endTemporarySession(t.Session); cleanupErr == nil {
		cleanupErr = err
	}
	return cleanupErr
}

func (e *Engine) beginLocked(op *session.Operation, t *session.TransactionOwner, state session.TxState, start time.Time) error {
	if t.Next == ids.TxID(^uint64(0)) {
		return errors.New("stuntgres: transaction identity exhausted")
	}
	sess := op.Session
	e.Mu.Lock()
	defer e.Mu.Unlock()
	settings := sess.Settings
	settings.Mu.Lock()
	defer settings.Mu.Unlock()
	sess.Mu.Lock()
	defer sess.Mu.Unlock()
	if err := PublicationOperationLocked(e, op); err != nil {
		return err
	}
	diagnostic, err := e.Visibility.NextDiagnosticTxLocked()
	if err != nil {
		return err
	}
	t.Next++
	t.Ref = e.Catalog.Domain.Tx(t.Session.SessionToken, t.Next, e.Catalog.State.Epoch(), t, diagnostic)
	PayloadOf(sess).notifications.Busy = true
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
	s := &StatementScope{Owner: owner, Op: op, statementStart: start}
	s.self = s
	s.lease.Statement = s
	s.Stage.scope = s
	s.Stage.self = &s.Stage
	s.Input, s.Output = op.Session.Settings.Presentation()
	// Only private Restore INSERT replay owns this authority. The same input
	// flows to literal analysis and execution-time coercions; ordinary scopes
	// (including statements after Restore) retain normal BOX normalization.
	s.Input.RawBoxReplay = OperationReplayOrZero(op).InsertAuthority
	settings := op.Session.Settings
	settings.Mu.Lock()
	s.RowSecurity = settings.Value("row_security") == "on"
	settings.Mu.Unlock()
	return s
}

func (s *StatementScope) Active() error {
	if s == nil || s.self != s || s.Finished {
		return errors.New("stuntgres: inactive statement scope")
	}
	return s.Engine().validateOperation(s.Op)
}

func (s *StatementScope) Gate(stmt parser.Statement) error {
	if err := s.Active(); err != nil {
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
		s.Owner.Mu.Lock()
		defer s.Owner.Mu.Unlock()
		if s.message {
			if err := s.admitTransactionLocked(s.Engine()); err != nil {
				return err
			}
		}
		if err := s.Engine().endPreflight(s.Op); err != nil {
			return err
		}
		if s.Owner.State == session.TxAborted && !session.AbortedControlAllowed(control) {
			return session.AbortedError()
		}
		return nil
	}
	t := s.Owner
	e := s.Engine()
	// Admission serializes this session, so the idle classification stays valid
	// while reserving replacement admission outside transaction synchronization.
	t.Mu.Lock()
	idle := t.State == session.TxIdle
	t.Mu.Unlock()
	if idle && session.ReplacementRead(stmt) {
		e.ReplacementMu.RLock()
		s.ReplacementRead = true
	}
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if err := s.admitTransactionLocked(e); err != nil {
		return err
	}
	e.Mu.Lock()
	stale := t.Ref.Epoch() != e.Catalog.State.Epoch()
	e.Mu.Unlock()
	if stale {
		return ids.ErrCatalogReplaced
	}
	if t.State == session.TxAborted {
		return session.AbortedError()
	}
	if t.Command == ids.CommandID(^uint64(0)) {
		return errors.New("stuntgres: command identity exhausted")
	}
	return nil
}

// The transaction owner mutex is held; admission protects the projected roots.
func (s *StatementScope) attachTransactionLocked() {
	t := s.Owner
	s.LockTicket = t.Ticket
	s.TxRef = t.Ref
	s.implicit = t.State == session.TxImplicit
	s.Input.TransactionStart = t.Start
	s.CommandID = t.Command
	s.OwnWrites = t.Writes
	s.OwnDeletes = t.Deletes
}

func (s *StatementScope) ResolutionCatalog() catalog.Read {
	if err := s.Active(); err != nil {
		panic(err)
	}
	return s.Engine().CatalogRead()
}

func (s *StatementScope) Writes() *WriteStage { return &s.Stage }

// Apply takes immutable payload ownership only after provenance and capacity
// checks. The binder supplied the original ObjectRef, not a reconstructed name.
func (w *WriteStage) Apply(ref ids.ObjectRef, rows [][]sqltype.Value) error {
	if w == nil || w.self != w || w.scope == nil {
		return errors.New("stuntgres: foreign or missing write stage")
	}
	s := w.scope
	if err := s.Active(); err != nil {
		return err
	}
	batch := index.WriteBatch{Values: rows}
	for i := range batch.Values {
		candidate := batch.Candidate(i)
		if err := dml.CheckUniqueRow(s.WriteHost(), ref, &candidate, nil, batch.Prefix(i)); err != nil {
			return err
		}
		batch.Set(i, candidate)
	}
	return dml.ApplyValidated(s.WriteHost(), ref, batch)
}

func (s *StatementScope) Describe(stmt parser.Statement) (Description, error) {
	if err := s.Gate(stmt); err != nil {
		return Description{}, err
	}
	if copy, ok := stmt.(*parser.Copy); ok {
		return Description{}, CopyBoundary(copy)
	}
	if _, ok := stmt.(*parser.SavepointControl); ok {
		s.controlOutcome = controlDescribed
		return Description{}, nil
	}
	if _, ok := stmt.(*parser.TransactionControl); ok {
		s.controlOutcome = controlDescribed
		return Description{}, nil
	}
	if transactionalRoleUtility(stmt) {
		// Selected role utilities have no preparation-time authority effects.
		return Description{}, nil
	}
	switch session.DescribeBeforeSnapshot(stmt) {
	case session.DescribeEmpty:
		return Description{}, nil
	case session.DescribeOperation:
		return s.Engine().describeOperation(s, stmt)
	}
	if session.TransactionalSchemaUtility(stmt) {
		if _, queryCreation := stmt.(*parser.CreateQueryTable); !queryCreation {
			return s.Engine().describeOperation(s, stmt)
		}
	}
	_, queryCreation := stmt.(*parser.CreateQueryTable)
	if queryCreation {
		if err := s.executionEligibility(stmt); err != nil {
			return Description{}, err
		}
	}
	if err := s.snapshotAnalysisPhase(stmt); err != nil {
		return Description{}, err
	}
	if session.DescribeAfterSnapshot(stmt) {
		// Definition utilities have no result signature. Resolve ownership, read-only
		// precedence and persistent-DDL exclusions only when actually executing.
		return s.Engine().describeOperation(s, stmt)
	}
	indexBuild := session.EarlyIndexUtility(stmt)
	if !queryCreation && s.Owner.State == session.TxActive && session.ExplicitUtility(stmt) && !indexBuild {
		return Description{}, session.UnsupportedInBlockError()
	}
	if err := s.AcquireRelation(stmt, false); err != nil {
		return Description{}, err
	}
	return s.Engine().describeOperation(s, stmt)
}

func (s *StatementScope) Execute(stmt parser.Statement, count int, importFrame FrameImporter) (result Result, err error) {
	return s.execute(stmt, count, importFrame, nil)
}

// One canonical resolver/importer/executor serves embedded and protocol callers.
func (s *StatementScope) execute(stmt parser.Statement, count int, importFrame FrameImporter, stream *protocolCopyExecution) (result Result, err error) {
	var before session.TxState
	if s != nil && s.Owner != nil {
		s.Owner.Mu.Lock()
		before = s.Owner.State
		s.Owner.Mu.Unlock()
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
		if err := s.Active(); err != nil {
			return Result{}, err
		}
		if err := s.parallelExecutionEligibility(stmt); err != nil {
			return Result{}, err
		}
		if count != 0 && !preparedInvocation {
			return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
		}
		if count != 0 || stream != nil && s.protocolOrZero().simple {
			// SQL EXECUTE is an outer utility: its portal exists before the
			// prepared-name lookup, actual arguments and inner planning.
			if err := s.Gate(stmt); err != nil {
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
			importErr := importFrame(s.executionContext(), formal, utilityFrame, s.Input)
			clear(utilityFrame)
			if importErr != nil {
				return Result{}, importErr
			}
		}
		entry := PayloadOf(s.Op.Session).PreparedSQL.Lookup(execute.Name.Text)
		if entry == nil {
			// Establish the ordinary failed-command transaction boundary too.
			if err := s.Gate(stmt); err != nil {
				return Result{}, err
			}
			return Result{}, missingSQLPrepared(execute.Name.Text)
		}
		stmt = entry.Body
		s.Expected = &entry.Description
		count = entry.Description.Parameters.Count
		_, copyBody := stmt.(*parser.Copy)
		nextExecute, executeBody := stmt.(*parser.ExecuteSQL)
		importFrame = s.sqlExecuteImporter(execute, executeBody || stream != nil && copyBody)
		preparedInvocation = true
		execute = nextExecute
	}
	base, counted, insert, ok := session.ClassifyCommandTag(stmt, before)
	if !ok {
		return Result{}, SQLError("0A000", "statement command tag is not supported")
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
	if err := s.Gate(stmt); err != nil {
		return Result{}, err
	}
	if err := s.parallelExecutionEligibility(stmt); err != nil {
		return Result{}, err
	}
	if s.protocolOrZero().simpleStreaming && !wrapped && session.ProtocolPlainSelect(stmt) {
		p := newProtocolPortal(PreparedDefinition{Body: stmt}, s.TxRef)
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
			if err := importFrame(s.executionContext(), formal, frame, s.Input); err != nil {
				return Result{}, err
			}
		}
		copied, copyErr := s.copyProtocol(copy, stream.source, stream.destination, stream.hooks)
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
		return s.Engine().executeStatement(s, stmt, count, importFrame)
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
		return s.Engine().executeStatement(s, stmt, count, importFrame)
	}
	_, queryCreation := stmt.(*parser.CreateQueryTable)
	if queryCreation && !session.TransactionalSchemaUtility(stmt) {
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
		return s.Engine().executeStatement(s, stmt, count, importFrame)
	}
	indexBuild := session.EarlyIndexUtility(stmt)
	_, refreshing := stmt.(*parser.RefreshMaterializedView)
	earlyEligibility := indexBuild || queryCreation || refreshing
	if earlyEligibility && !queryCreation {
		if err := s.executionEligibility(stmt); err != nil {
			return Result{}, err
		}
	}
	if tag, eligible := session.EventCommand(stmt); eligible {
		if !earlyEligibility {
			if err := s.executionEligibility(stmt); err != nil {
				return Result{}, err
			}
			earlyEligibility = true
		}
		if !queryCreation && count != 0 {
			return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
		}
		previous, beginErr := s.BeginEventCommand(tag)
		if beginErr != nil {
			return Result{}, beginErr
		}
		defer func() { err = s.FinishEventCommand(previous, err) }()
	}
	if err := s.AcquireRelation(stmt, true); err != nil {
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
			e := s.Engine()
			e.Mu.Lock()
			err := e.VisibilityWriteLocked(s.TxRef)
			e.Mu.Unlock()
			if err != nil {
				return Result{}, err
			}
		}
	}
	return s.Engine().executeStatement(s, stmt, count, importFrame)
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
	if PayloadOf(t.Session).PreparedSQL == nil {
		return err
	}
	for name, p := range PayloadOf(t.Session).PreparedSQL.Bindings() {
		if p.Retired {
			PayloadOf(t.Session).PreparedSQL.RemoveBinding(name)
			var failure error
			if wire {
				failure = scope.protocolTerminalIO(func() error { return p.Close(ctx) })
			} else {
				failure = p.Close(ctx)
			}
			if err == nil {
				err = failure
			}
		}
	}
	return err
}

// ScopeOfLease returns the statement scope that holds l, or nil.
func ScopeOfLease(l *session.StatementLease) *StatementScope {
	if l == nil {
		return nil
	}
	s, _ := l.Statement.(*StatementScope)
	return s
}

// ActiveScopeOfLease returns only unfinished statement authority. A finished
// lease remains available for protocol/timeout bookkeeping, not catalog mutation.
func ActiveScopeOfLease(l *session.StatementLease) *StatementScope {
	s := ScopeOfLease(l)
	if s == nil || s.Finished {
		return nil
	}
	return s
}
