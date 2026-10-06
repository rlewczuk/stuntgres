package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

const ReadCommitted Isolation = 0

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
	admission sync.Mutex
	ticket    *relationTicket
	mu        sync.Mutex
	session   *Session
	state     TxState
	ref       TxRef
	next      TxID
	command   CommandID
	writes    map[ObjectAddress][]rowVersion
	deletes   map[ObjectAddress]*privateDeletionRoot
	start     time.Time
}

func (t *transactionOwner) clear() {
	t.state = TxIdle
	t.ref = TxRef{}
	t.command = 0
	t.writes = nil
	t.deletes = nil
	t.start = time.Time{}
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
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return err
	}
	t.next++
	t.ref = TxRef{owner: e, session: t.session.token, id: t.next, epoch: e.state.epoch, attachment: t}
	t.state = state
	t.start = time.Now().UTC()
	return nil
}

func (e *Engine) Begin(op *Operation, options TxOptions) (TxRef, error) {
	if options.Isolation != ReadCommitted {
		return TxRef{}, errors.New("stuntgres: unsupported transaction isolation level")
	}
	if options.ReadOnly {
		return TxRef{}, errors.New("stuntgres: read-only transactions are unsupported")
	}
	if err := e.transactionAdmission(op); err != nil {
		return TxRef{}, err
	}
	t := op.session.transactions
	t.admission.Lock()
	defer t.admission.Unlock()
	return e.beginAdmitted(op, t)
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
// fresh READ COMMITTED view. Writes stage effects here, never in storage.
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
	sourceRef          ObjectRef
	sourceDescriptor   Descriptor
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
	s.input = sqltype.InputEnvironment{TransactionStart: t.start}
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
	if t.state == TxActive {
		switch stmt.(type) {
		case *parser.Comment, *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableNotNull, *parser.AlterTableDropColumn, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateTable, *parser.DropTable, *parser.CreateRole, *parser.DropRole, *parser.SessionAuthorization:
			return sqlError("0A000", "statement is unsupported in an explicit transaction")
		}
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
	if _, ok := stmt.(*parser.TransactionControl); ok {
		s.controlOutcome = controlDescribed
		return StatementDescription{}, nil
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
	if control, ok := stmt.(*parser.TransactionControl); ok {
		return s.executeControl(control, count)
	}
	if _, ok := stmt.(*parser.LockTable); ok && count != 0 {
		return Result{}, errors.New("stuntgres: LOCK TABLE takes no arguments")
	}
	if err := s.acquireRelation(stmt, true); err != nil {
		return Result{}, err
	}
	if _, ok := stmt.(*parser.LockTable); ok {
		return Result{}, nil
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
		t.session.settings.finishTransaction(false)
		t.state = TxAborted
		t.writes = nil
		t.deletes = nil
		endedTicket = t.ticket
		t.ticket = nil
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

// VisibilityView retains frozen relation-local slice prefixes, not copied row
// values. Each handle has independent release authority. The immutable data is
// naturally reclaimed after the last view/result owner; no registry is needed.
type visibilityData struct {
	tx        TxRef
	command   CommandID
	frontier  CommitSeq
	catalog   CatalogRead
	committed map[ObjectAddress][]rowVersion
	storage   *relationStorage // Exact shape generation of this one-relation view.
	own       map[ObjectAddress][]rowVersion
	ends      map[ObjectAddress]*deletionRoot
	deleted   map[ObjectAddress]*privateDeletionRoot
}

type VisibilityView struct {
	self *VisibilityView
	data atomic.Pointer[visibilityData]
}

func newVisibilityView(data *visibilityData) *VisibilityView {
	v := &VisibilityView{}
	v.self = v
	v.data.Store(data)
	return v
}

func (s *StatementScope) AcquireDataView(ref ObjectRef) (*VisibilityView, error) {
	if err := s.active(); err != nil {
		return nil, err
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if !s.worked || s.owner.state == TxIdle || s.owner.state == TxAborted {
		return nil, errors.New("stuntgres: inactive data view scope")
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.acquireDataViewLocked(e, ref)
}

func (s *StatementScope) acquireDataViewLocked(e *Engine, ref ObjectRef) (*VisibilityView, error) {
	if ref.owner != e.owner {
		return nil, errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.state.epoch || s.txRef.epoch != e.state.epoch {
		return nil, ErrCatalogReplaced
	}
	storage := e.storage[ref.Address]
	if storage == nil {
		return nil, errors.New("stuntgres: relation storage is missing")
	}
	if s.view != nil {
		if s.view.data.Load() == nil {
			return nil, errors.New("stuntgres: visibility view is released")
		}
		if _, ok := s.view.read().committed[ref.Address]; !ok {
			return nil, errors.New("stuntgres: relation is outside statement data view")
		}
		return s.view, nil
	}
	if s.txRef.epoch != e.state.epoch {
		return nil, ErrCatalogReplaced
	}
	data := &visibilityData{
		tx: s.txRef, command: s.command, frontier: e.commitSeq,
		catalog:   CatalogRead{owner: e, state: e.state},
		committed: map[ObjectAddress][]rowVersion{ref.Address: storage.versions},
		storage:   storage,
	}
	if storage.ends != nil {
		data.ends = map[ObjectAddress]*deletionRoot{ref.Address: storage.ends}
	}
	data.deleted = s.ownDeletes
	if rows := s.ownWrites[ref.Address]; len(rows) != 0 {
		data.own = map[ObjectAddress][]rowVersion{ref.Address: rows}
	}
	s.view = newVisibilityView(data)
	return s.view, nil
}

func (v *VisibilityView) Retain() *VisibilityView {
	if v == nil || v.self != v {
		return nil
	}
	data := v.data.Load()
	if data == nil {
		return nil
	}
	return newVisibilityView(data)
}

func (v *VisibilityView) Release() {
	if v != nil && v.self == v {
		v.data.Store(nil)
	}
}

func (v *VisibilityView) read() *visibilityData {
	if v == nil || v.self != v {
		panic("stuntgres: visibility view is released")
	}
	data := v.data.Load()
	if data == nil {
		panic("stuntgres: visibility view is released")
	}
	return data
}

func (v *VisibilityView) Tx() TxRef            { return v.read().tx }
func (v *VisibilityView) Command() CommandID   { return v.read().command }
func (v *VisibilityView) Frontier() CommitSeq  { return v.read().frontier }
func (v *VisibilityView) Catalog() CatalogRead { return v.read().catalog }

// Prepare immutable replacement heads/end roots outside final locks. Appends
// retain geometric capacity; final installation follows one all-relations veto.
type rowPublication struct {
	address            ObjectAddress
	store              *relationStorage
	relation           *relationDescriptor
	root, next, writes []rowVersion
	ends, nextEnds     *deletionRoot
	deletion           *privateDeletionRoot
	generation         uint64
	slots              []int
	singleSlot         int
}

func (e *Engine) publishWrites(op *Operation, ref TxRef, writes map[ObjectAddress][]rowVersion, deletes map[ObjectAddress]*privateDeletionRoot) error {
	if op == nil || op.self != op || op.session == nil || op.session.transactions == nil || ref.attachment == nil || ref.owner != e || ref.session != op.session.token || ref.attachment != op.session.transactions || ref.id == 0 || ref != ref.attachment.ref {
		return errors.New("stuntgres: foreign or stale publication transaction")
	}
	if ref.attachment.state != TxActive && ref.attachment.state != TxImplicit {
		return errors.New("stuntgres: inactive publication transaction")
	}
	if len(writes) == 0 && len(deletes) == 0 {
		e.mu.Lock()
		defer e.mu.Unlock()
		op.session.mu.Lock()
		defer op.session.mu.Unlock()
		if err := publicationOperationLocked(e, op); err != nil {
			return err
		}
		if ref.epoch != e.state.epoch {
			return ErrCatalogReplaced
		}
		return nil
	}
	for {
		prepared := make([]rowPublication, 0, len(writes)+len(deletes))
		e.mu.Lock()
		if ref.epoch != e.state.epoch {
			e.mu.Unlock()
			return ErrCatalogReplaced
		}
		capture := func(a ObjectAddress, rows []rowVersion, deletion *privateDeletionRoot) error {
			store := e.storage[a]
			relation, live := e.state.descriptors[a].(*relationDescriptor)
			if store == nil || !live || a.SubID != 0 {
				return ErrCatalogReplaced
			}
			prepared = append(prepared, rowPublication{address: a, store: store, relation: relation, root: store.versions, writes: rows, ends: store.ends, deletion: deletion, generation: store.generation, singleSlot: -1})
			return nil
		}
		var err error
		for a, rows := range writes {
			if err = capture(a, rows, deletes[a]); err != nil {
				break
			}
		}
		if err == nil {
			for a, deletion := range deletes {
				if _, written := writes[a]; written {
					continue
				}
				if err = capture(a, nil, deletion); err != nil {
					break
				}
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		for i := range prepared {
			p := &prepared[i]
			inserts, replacements := 0, 0
			for _, row := range p.writes {
				if row.row == 0 || row.version == 0 || row.born != 0 || len(row.values) != len(p.relation.columns) {
					return errors.New("stuntgres: invalid private row version")
				}
				if row.base == 0 {
					inserts++
				} else {
					replacements++
				}
			}
			size := len(p.root) + inserts
			if replacements == 0 && size <= cap(p.root) {
				p.next = p.root[:size]
			} else {
				capacity := max(size, cap(p.root)*2)
				if replacements != 0 {
					capacity = size
				}
				p.next = make([]rowVersion, size, capacity)
				copy(p.next, p.root)
			}
			if replacements != 0 {
				var index map[RowID]int
				if replacements > 1 {
					index = make(map[RowID]int, len(p.root))
					for slot, row := range p.root {
						index[row.row] = slot
					}
				}
				if len(p.writes) > 1 {
					p.slots = make([]int, len(p.writes))
				}
				tail := len(p.root)
				for j, row := range p.writes {
					if row.base == 0 {
						p.slots[j] = tail
						tail++
						continue
					}
					var predecessor rowVersion
					var slot int
					var found bool
					if index != nil {
						slot, found = index[row.row]
						if found {
							predecessor = p.root[slot]
						}
					} else {
						predecessor, slot, found = rowByID(p.root, row.row, -1)
					}
					if !found || predecessor.version != row.base || !liveVersion(predecessor, CommitSeq(^uint64(0)), p.ends, nil) {
						return errors.New("stuntgres: stale update predecessor")
					}
					if p.slots == nil {
						p.singleSlot = slot
					} else {
						p.slots[j] = slot
					}
				}
			}
			if p.deletion != nil {
				count := len(p.deletion.rows)
				if p.ends != nil {
					count += len(p.ends.rows)
				}
				p.nextEnds = &deletionRoot{rows: make(map[RowID]rowEnd, count)}
				if p.ends != nil {
					for id, end := range p.ends.rows {
						p.nextEnds.rows[id] = end
					}
				}
				for id, candidate := range p.deletion.rows {
					if candidate.ref.owner != e.owner || candidate.ref.Epoch != ref.epoch || candidate.ref.Address != p.address || id == 0 || id != candidate.row || candidate.version == 0 || candidate.storage != p.store {
						return ErrCatalogReplaced
					}
					predecessors := p.root
					if candidate.own {
						predecessors = p.writes
					}
					predecessor, _, found := rowByID(predecessors, id, candidate.ordinal)
					if !found || predecessor.version != candidate.version || len(predecessor.values) != len(p.relation.columns) {
						return errors.New("stuntgres: invalid deletion predecessor")
					}
					if !candidate.own && !liveVersion(predecessor, CommitSeq(^uint64(0)), p.ends, nil) {
						return errors.New("stuntgres: deletion predecessor already ended")
					}
					p.nextEnds.rows[id] = rowEnd{version: candidate.version}
				}
			}
		}
		session := op.session
		e.mu.Lock()
		session.mu.Lock()
		err = publicationOperationLocked(e, op)
		if err == nil && ref.epoch != e.state.epoch {
			err = ErrCatalogReplaced
		}
		if err == nil && e.commitSeq == CommitSeq(^uint64(0)) {
			err = errors.New("stuntgres: commit sequence exhausted")
		}
		changed := false
		if err == nil {
			for _, p := range prepared {
				if e.storage[p.address] != p.store || p.store.generation != p.generation || p.store.ends != p.ends || len(p.store.versions) != len(p.root) {
					changed = true
					break
				}
				current, ok := e.state.descriptors[p.address].(*relationDescriptor)
				if !ok || !sameRelationRowShape(p.relation, current) {
					err = ErrCatalogReplaced
					break
				}
				if p.store.generation == ^uint64(0) {
					err = errors.New("stuntgres: storage generation exhausted")
					break
				}
			}
		}
		if err != nil {
			session.mu.Unlock()
			e.mu.Unlock()
			return err
		}
		if changed {
			session.mu.Unlock()
			e.mu.Unlock()
			continue
		}
		// No callback, allocation or failure remains after this point.
		for _, p := range prepared {
			for i, row := range p.writes {
				slot := len(p.root) + i
				if p.slots != nil {
					slot = p.slots[i]
				}
				if p.singleSlot >= 0 {
					slot = p.singleSlot
				}
				row.born, row.base = e.commitSeq+1, 0
				p.next[slot] = row
			}
			if p.nextEnds != nil {
				for id, candidate := range p.deletion.rows {
					p.nextEnds.rows[id] = rowEnd{version: candidate.version, end: e.commitSeq + 1}
				}
				p.store.ends = p.nextEnds
			}
			p.store.versions = p.next
			p.store.generation++
		}
		e.commitSeq++
		session.mu.Unlock()
		e.mu.Unlock()
		return nil
	}
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
