package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

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
	values  []sqltype.Value
}

type transactionOwner struct {
	mu      sync.Mutex
	session *Session
	state   TxState
	ref     TxRef
	next    TxID
	command CommandID
	writes  map[ObjectAddress][]rowVersion
}

func (t *transactionOwner) clear() {
	t.state = TxIdle
	t.ref = TxRef{}
	t.command = 0
	t.writes = nil
}

func (t *transactionOwner) EndSession(token SessionToken, _ EndReason) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if token != t.session.token {
		return errors.New("stuntgres: foreign transaction session")
	}
	t.clear()
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
	session.mu.Lock()
	defer session.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return err
	}
	t.next++
	t.ref = TxRef{owner: e, session: t.session.token, id: t.next, epoch: e.state.epoch, attachment: t}
	t.state = state
	return nil
}

func (e *Engine) Begin(op *Operation, options TxOptions) (TxRef, error) {
	if options.Isolation != ReadCommitted {
		return TxRef{}, errors.New("stuntgres: unsupported transaction isolation level")
	}
	if options.ReadOnly {
		return TxRef{}, errors.New("stuntgres: read-only transactions are unsupported")
	}
	if err := e.validateOperation(op); err != nil {
		return TxRef{}, err
	}
	t := op.session.transactions
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := e.validateOperation(op); err != nil {
		return TxRef{}, err
	}
	if t.state != TxIdle {
		return TxRef{}, errors.New("stuntgres: transaction already active")
	}
	if err := e.beginLocked(op, t, TxActive); err != nil {
		return TxRef{}, err
	}
	return t.ref, nil
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
	if err := e.validateOperation(op); err != nil {
		return EndResult{}, err
	}
	t := op.session.transactions
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validate(ref); err != nil {
		return EndResult{}, err
	}
	t.clear()
	return EndResult{}, nil
}

func (e *Engine) Commit(op *Operation, ref TxRef) (EndResult, error) {
	admissionErr := e.validateOperation(op)
	if admissionErr != nil && !errors.Is(admissionErr, context.Canceled) && !errors.Is(admissionErr, context.DeadlineExceeded) {
		return EndResult{}, admissionErr
	}
	t := op.session.transactions
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validate(ref); err != nil {
		return EndResult{}, err
	}
	// An admitted commit failure ends this incarnation. Invalid references and
	// copied/done operations above never discard another transaction's work.
	defer t.clear()
	if err := e.validateOperation(op); err != nil {
		return EndResult{}, err
	}
	e.mu.Lock()
	stale := ref.epoch != e.state.epoch
	e.mu.Unlock()
	if stale {
		return EndResult{}, ErrCatalogReplaced
	}
	if t.state == TxAborted {
		return EndResult{}, nil
	}
	if err := e.publishWrites(op, ref, t.writes); err != nil {
		return EndResult{}, err
	}
	return EndResult{Committed: true}, nil
}

// StatementScope serializes one session's statement, from parsing through Finish.
// Binding uses current resolution metadata; data consumers explicitly acquire a
// fresh READ COMMITTED view. Future consumers stage effects here, never in storage.
type StatementScope struct {
	self            *StatementScope
	owner           *transactionOwner
	op              *Operation
	stage           WriteStage
	view            *VisibilityView
	finished        bool
	result          error
	worked          bool
	completed       bool
	replacementRead bool
}

type WriteStage struct {
	self  *WriteStage
	scope *StatementScope
	rows  map[ObjectAddress][]rowVersion
}

func (e *Engine) StartStatement(op *Operation) (*StatementScope, error) {
	if err := e.validateOperation(op); err != nil {
		return nil, err
	}
	t := op.session.transactions
	t.mu.Lock()
	if err := e.validateOperation(op); err != nil {
		t.mu.Unlock()
		return nil, err
	}
	s := &StatementScope{owner: t, op: op}
	s.self = s
	s.stage.scope = s
	s.stage.self = &s.stage
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
	t := s.owner
	e := s.op.session.Engine()
	if t.state == TxIdle {
		// Only callback-free row work retains replacement admission. CREATE
		// validates providers outside publication locks; a provider may Reset.
		switch stmt.(type) {
		case *parser.Insert, *parser.Select:
			e.replacementMu.RLock()
			s.replacementRead = true
		}
		if err := e.beginLocked(s.op, t, TxImplicit); err != nil {
			return err
		}
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
	if t.state == TxActive {
		switch stmt.(type) {
		case *parser.CreateTable, *parser.Set, *parser.Show:
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
	n := uint64(len(rows))
	if n > ^uint64(0)-e.nextRow {
		return errors.New("stuntgres: row identity exhausted")
	}
	versions := make([]rowVersion, len(rows))
	for i, row := range rows {
		e.nextRow++
		versions[i] = rowVersion{row: RowID(e.nextRow), version: VersionID(e.nextRow), values: row}
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
	return s.op.session.Engine().describeOperation(s.op, stmt)
}

func (s *StatementScope) Execute(stmt parser.Statement, count int, importFrame FrameImporter) (Result, error) {
	// Adapter argument normalization precedes scope admission.
	if err := s.gate(stmt); err != nil {
		return Result{}, err
	}
	return s.op.session.Engine().executeStatement(s, stmt, count, importFrame)
}

func (s *StatementScope) Finish(err error) error {
	if s == nil || s.self != s {
		return errors.New("stuntgres: foreign or missing statement scope")
	}
	if s.finished {
		return s.result
	}
	t := s.owner
	e := s.op.session.Engine()
	// Parsing can fail before classification; admitted failures still abort
	// explicit work. Completed row/catalog/session utilities have already passed
	// their installation preflight; later cancellation cannot retract success.
	if err == nil && s.worked && !s.completed {
		err = e.validateOperation(s.op)
	}
	if err == nil && s.worked {
		if t.state == TxImplicit {
			if !s.completed {
				err = e.publishWrites(s.op, t.ref, s.stage.rows)
			}
		} else {
			session := s.op.session
			session.mu.Lock()
			e.mu.Lock()
			err = publicationOperationLocked(e, s.op)
			if err == nil && t.ref.epoch != e.state.epoch {
				err = ErrCatalogReplaced
			}
			if err == nil {
				if len(s.stage.rows) != 0 && t.writes == nil {
					t.writes = make(map[ObjectAddress][]rowVersion)
				}
				for a, rows := range s.stage.rows {
					if len(t.writes[a]) == 0 {
						t.writes[a] = rows
					} else {
						t.writes[a] = append(t.writes[a], rows...)
					}
				}
			}
			e.mu.Unlock()
			session.mu.Unlock()
		}
		if err == nil {
			t.command++
		}
	}
	var diagnostic *sqlstate.Error
	admittedFailure := s.worked || errors.As(err, &diagnostic) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	if t.state == TxImplicit {
		t.clear()
	} else if err != nil && admittedFailure && (t.state == TxActive || t.state == TxAborted) {
		t.state = TxAborted
		t.writes = nil
	}
	if s.view != nil {
		s.view.Release()
		s.view = nil
	}
	s.stage.rows = nil
	if s.replacementRead {
		e.replacementMu.RUnlock()
		s.replacementRead = false
	}
	s.finished = true
	s.result = err
	t.mu.Unlock()
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
	own       map[ObjectAddress][]rowVersion
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
	if ref.Epoch != e.state.epoch || s.owner.ref.epoch != e.state.epoch {
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
	if s.owner.ref.epoch != e.state.epoch {
		return nil, ErrCatalogReplaced
	}
	data := &visibilityData{
		tx: s.owner.ref, command: s.owner.command, frontier: e.commitSeq,
		catalog:   CatalogRead{owner: e, state: e.state},
		committed: map[ObjectAddress][]rowVersion{ref.Address: storage.versions},
	}
	if rows := s.owner.writes[ref.Address]; len(rows) != 0 {
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

// Publication grows vectors geometrically, outside final locks. Spare capacity
// is written only after final preflight, under Engine.mu; captured prefixes are
// never rewritten. A peer append causes re-preparation, not SQL re-execution.
func (e *Engine) publishWrites(op *Operation, ref TxRef, writes map[ObjectAddress][]rowVersion) error {
	if len(writes) == 0 {
		session := op.session
		session.mu.Lock()
		defer session.mu.Unlock()
		e.mu.Lock()
		defer e.mu.Unlock()
		if err := publicationOperationLocked(e, op); err != nil {
			return err
		}
		if ref.epoch != e.state.epoch {
			return ErrCatalogReplaced
		}
		return nil
	}
	for {
		e.mu.Lock()
		if ref.epoch != e.state.epoch {
			e.mu.Unlock()
			return ErrCatalogReplaced
		}
		if e.commitSeq == CommitSeq(^uint64(0)) {
			e.mu.Unlock()
			return errors.New("stuntgres: commit sequence exhausted")
		}
		roots := make(map[ObjectAddress][]rowVersion, len(writes))
		stores := make(map[ObjectAddress]*relationStorage, len(writes))
		for a := range writes {
			storage := e.storage[a]
			if storage == nil {
				e.mu.Unlock()
				return errors.New("stuntgres: relation storage is missing")
			}
			roots[a] = storage.versions
			stores[a] = storage
		}
		e.mu.Unlock()
		next := make(map[ObjectAddress][]rowVersion, len(writes))
		for a, rows := range writes {
			root := roots[a]
			size := len(root) + len(rows)
			if size <= cap(root) {
				next[a] = root[:size]
				continue
			}
			capacity := cap(root) * 2
			if capacity < size {
				capacity = size
			}
			dst := make([]rowVersion, size, capacity)
			copy(dst, root)
			next[a] = dst
		}
		session := op.session
		session.mu.Lock()
		e.mu.Lock()
		err := publicationOperationLocked(e, op)
		if err == nil && ref.epoch != e.state.epoch {
			err = ErrCatalogReplaced
		}
		if err == nil && e.commitSeq == CommitSeq(^uint64(0)) {
			err = errors.New("stuntgres: commit sequence exhausted")
		}
		if err != nil {
			e.mu.Unlock()
			session.mu.Unlock()
			return err
		}
		changed := false
		for a, storage := range stores {
			if e.storage[a] != storage || len(storage.versions) != len(roots[a]) {
				changed = true
			}
		}
		if changed {
			e.mu.Unlock()
			session.mu.Unlock()
			continue
		}
		// All fallible work and allocations are complete. One critical section
		// installs every relation and frontier; no callback can see a half batch.
		for a, rows := range next {
			copy(rows[len(roots[a]):], writes[a])
			for i := len(roots[a]); i < len(rows); i++ {
				rows[i].born = e.commitSeq + 1
			}
			stores[a].versions = rows
		}
		e.commitSeq++
		e.mu.Unlock()
		session.mu.Unlock()
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
