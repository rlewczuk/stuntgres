package core

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Query ownership is independent of lexical mutation scratch. Writable CTEs
// share a frame; SQL reached from a routine has its own effectCommand frame.
// Admission serializes all access; no evaluator runs under an engine mutex.
type afterTriggerState struct {
	frames map[uint64]*afterTriggerFrame
}

type afterTriggerFrame struct {
	rows, statements []afterTriggerEvent
	row, statement   int
	draining         bool
	producers        []afterStatementProducer
	producer         int
	before           map[afterStatementKey]struct{}
}

type afterStatementKey struct {
	relation ids.ObjectRef
	event    stored.TriggerEvent
}

// Candidate lists are immutable once a successful eager producer retains them.
type afterStatementProducer struct {
	key        afterStatementKey
	candidates []afterTriggerEvent
}

type afterTriggerEvent struct {
	definition stored.Trigger
	relation   *catalog.Relation
	read       catalog.Read
	event      stored.TriggerEvent
	old, next  []sqltype.Value
	actor      *AuthorizationView // Immutable RI authority; live grants are recaptured.
	qualified  bool               // Statement WHEN is decided once at producer completion.
	skip       bool
}

func (s *StatementScope) afterFrame() *afterTriggerFrame {
	if s.afterTriggers == nil {
		s.afterTriggers = &afterTriggerState{frames: make(map[uint64]*afterTriggerFrame)}
	}
	f := s.afterTriggers.frames[s.effectCommand]
	if f == nil {
		f = &afterTriggerFrame{}
		s.afterTriggers.frames[s.effectCommand] = f
	}
	return f
}

func (h *statementHost) AfterRow(request dml.BeforeRow) error {
	return h.queueAfter(request.Relation, stored.TriggerEvent(request.Event), stored.TriggerRow, request.Targets, request.Old, request.New)
}

func (h *statementHost) AfterStatement(ref ids.ObjectRef, event stored.TriggerEvent, targets []int32) error {
	return h.queueAfter(ref, event, stored.TriggerStatement, targets, nil, nil)
}

func (h *statementHost) CompleteAfterStatement(ref ids.ObjectRef, event stored.TriggerEvent) error {
	s := h.scope()
	key := afterStatementKey{ref, event}
	if s.deferAfterProducers && s.deferredAfterCommand == s.effectCommand {
		if s.afterTriggers == nil || s.afterTriggers.frames[s.effectCommand] == nil {
			return nil
		}
		f := s.afterTriggers.frames[s.effectCommand]
		producer := afterStatementProducer{key: key}
		for _, candidate := range f.statements {
			if !candidate.qualified && candidate.definition.Parts().Relation == ref && candidate.event == event {
				producer.candidates = append(producer.candidates, candidate)
			}
		}
		f.producers = append(f.producers, producer)
		return nil
	}
	return s.qualifyAfterStatementsFor(&key)
}

func (h *statementHost) queueAfter(ref ids.ObjectRef, event stored.TriggerEvent, level stored.TriggerLevel, targets []int32, old, next []sqltype.Value) error {
	s := h.scope()
	if err := s.Active(); err != nil {
		return err
	}
	e := s.Engine()
	if level == stored.TriggerRow {
		if target, _ := e.Locks.ReplacementGrants(s.LockTicket, ref, nil); !target {
			return fmt.Errorf("stuntgres: trigger invocation requires the admitted physical write target")
		}
	}
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	relation, ok := auth.Catalog.Relation(ref.Address)
	if !ok || auth.Ref(ref.Address) != ref {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	definitions := stored.TriggersOf(relation)
	e.Mu.Unlock()

	var f *afterTriggerFrame
	key := afterStatementKey{ref, event}
	var ownedOld, ownedNext []sqltype.Value
	owned := false
	if level == stored.TriggerStatement && s.afterTriggers != nil {
		if prior := s.afterTriggers.frames[s.effectCommand]; prior != nil {
			prior.cancelStatements(key)
		}
	}
	for _, definition := range definitions {
		p := definition.Parts()
		if p.Timing != stored.TriggerAfter || p.Level != level || level == stored.TriggerRow && !definition.Mode().Fires(auth.RuleReplica()) || !definition.Fires(event, targets) {
			continue
		}
		if f == nil {
			f = s.afterFrame()
		}
		queued := afterTriggerEvent{
			definition: definition, relation: relation, read: auth.Catalog,
			event: event, old: old, next: next, actor: s.ForeignActionAuth,
		}
		if level == stored.TriggerRow {
			// ROW WHEN belongs to acceptance; STATEMENT WHEN to producer completion.
			pass, err := s.afterWhen(queued, auth)
			if err != nil {
				return err
			}
			if !pass {
				continue
			}
			if !owned {
				ownedOld, ownedNext, owned = slices.Clone(old), slices.Clone(next), true
			}
			queued.old, queued.next = ownedOld, ownedNext
			f.rows = append(f.rows, queued)
		} else {
			f.registerStatement(key, queued)
		}
	}
	return nil
}

func (s *StatementScope) afterWhen(event afterTriggerEvent, auth AuthorizationView) (bool, error) {
	if err := s.Op.Context().Err(); err != nil {
		return false, err
	}
	p := event.definition.Parts()
	if p.When == nil {
		return true, nil
	}
	old := exec.Context{
		EvalFrame: exec.EvalFrame{Scope: analyze.RulePseudoScope, Frame: 1, Row: event.old},
		EvalStatement: exec.EvalStatement{
			Context: s.Op.Context(), Host: s.WriteHost(), Catalog: event.read,
			Input: s.Input, Output: s.Output,
		},
	}
	old.RoutineAuthority, old.RoutineRecipe = &auth, true
	old.RoutineChecks, old.RoutineConsumer = p.Binding.Dependencies, event.relation
	next := old
	next.Frame, next.Row, next.Outer = 2, event.next, &old
	if err := exec.AdmitExpression(&next, p.When, event.read); err != nil {
		return false, err
	}
	value, err := p.When.Eval(&next)
	if err != nil || value.IsNull() {
		return false, err
	}
	return value.Boolean()
}

// Qualify early statement registrations after the producer is exhausted, before
// immediate RI work or queued row bodies can change the condition's view.
// Keep rejected registrations for deduplication and checkpoint their decision.
func (s *StatementScope) qualifyAfterStatements() error {
	if s.afterTriggers != nil {
		if f := s.afterTriggers.frames[s.effectCommand]; f != nil && !f.draining {
			for f.producer < len(f.producers) {
				producer := f.producers[f.producer]
				if err := s.qualifyAfterProducer(producer); err != nil {
					return err
				}
				f.producer++
			}
		}
	}
	return s.qualifyAfterStatementsFor(nil)
}

func (s *StatementScope) qualifyAfterProducer(producer afterStatementProducer) error {
	f := s.afterFrame()
	f.cancelStatements(producer.key)
	for _, candidate := range producer.candidates {
		f.registerStatement(producer.key, candidate)
	}
	return s.qualifyAfterStatementsFor(&producer.key)
}

func (s *StatementScope) qualifyAfterStatementsFor(key *afterStatementKey) error {
	if s.afterTriggers == nil {
		return nil
	}
	f := s.afterTriggers.frames[s.effectCommand]
	if f == nil || f.draining {
		return nil
	}
	for i := range f.statements {
		event := f.statements[i]
		if event.qualified || key != nil && (event.definition.Parts().Relation != key.relation || event.event != key.event) {
			continue
		}
		pass, err := s.qualifyAfterStatement(event)
		if err != nil {
			return err
		}
		f.statements[i].qualified, f.statements[i].skip = true, !pass
	}
	return nil
}

func (s *StatementScope) qualifyAfterStatement(event afterTriggerEvent) (bool, error) {
	previous := s.ForeignActionAuth
	s.ForeignActionAuth = event.actor
	defer func() { s.ForeignActionAuth = previous }()
	e := s.Engine()
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	e.Mu.Unlock()
	if err != nil {
		return false, err
	}
	if !event.definition.Mode().Fires(auth.RuleReplica()) {
		return false, nil
	}
	return s.afterWhen(event, auth)
}

// fireTrigger shares live executable admission and balanced invocation for
// queued AFTER and immediate BEFORE STATEMENT events; it never transforms rows.
func (s *StatementScope) fireTrigger(event afterTriggerEvent) error {
	if err := s.Op.Context().Err(); err != nil {
		return err
	}
	previous := s.ForeignActionAuth
	s.ForeignActionAuth = event.actor
	defer func() { s.ForeignActionAuth = previous }()
	p := event.definition.Parts()
	e := s.Engine()
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	var d *catalog.Procedure
	if err == nil && auth.Ref(p.Procedure.Address) != p.Procedure {
		err = fmt.Errorf("stuntgres: stale trigger function identity")
	}
	if err == nil {
		d, _ = auth.Catalog.Get(p.Procedure.Address).(*catalog.Procedure)
		if !catalog.SupportsDMLTrigger(d) {
			err = fmt.Errorf("stuntgres: invalid installed trigger function")
		} else {
			_, err = exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, d)
		}
	}
	if err == nil && !catalog.NativeDMLTrigger(d) {
		d, err = e.PreparePLRoutineLocked(s.Op, auth, d)
	}
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	if catalog.NativeDMLTrigger(d) {
		result, err := s.invokeNativeTrigger(d, event.relation, p, event.event, event.next, event.old, nil)
		if p.Timing == stored.TriggerBefore && p.Level == stored.TriggerStatement {
			return beforeStatementNativeResult(result, err)
		}
		return err
	}
	rel, err := plTriggerRelation(event.read, event.relation)
	if err != nil {
		return err
	}
	name := "INSERT"
	switch event.event {
	case stored.TriggerUpdate:
		name = "UPDATE"
	case stored.TriggerDelete:
		name = "DELETE"
	case stored.TriggerTruncate:
		name = "TRUNCATE"
	}
	level := "ROW"
	if p.Level == stored.TriggerStatement {
		level = "STATEMENT"
	}
	timing := "AFTER"
	if p.Timing == stored.TriggerBefore {
		timing = "BEFORE"
	}
	frame, err := proc.NewPLTriggerFrame(d.SQL, rel, p.Name, name, timing, level, p.Arguments, event.old, event.next)
	if err != nil {
		return err
	}
	defer frame.Close()
	if err := s.enterSQLRoutine(); err != nil {
		return err
	}
	defer s.leaveSQLRoutine()
	_, err = s.invokePLTrigger(d, frame, auth.Catalog, event.relation)
	return err
}

func (s *StatementScope) drainAfter() error {
	if s.afterTriggers == nil {
		return nil
	}
	f := s.afterTriggers.frames[s.effectCommand]
	if f == nil || f.draining {
		return nil
	}
	f.draining = true
	defer func() { f.draining = false }()
	for f.row < len(f.rows) || f.statement < len(f.statements) {
		var event afterTriggerEvent
		if f.row < len(f.rows) {
			event = f.rows[f.row]
			f.row++
		} else {
			event = f.statements[f.statement]
			f.statement++
			if event.skip {
				continue
			}
		}
		if err := s.fireTrigger(event); err != nil {
			return err
		}
	}
	return nil
}

// Checkpoints retain immutable event payloads but detach all mutable progress.
func cloneAfterState(state *afterTriggerState) *afterTriggerState {
	if state == nil {
		return nil
	}
	copy := &afterTriggerState{frames: make(map[uint64]*afterTriggerFrame, len(state.frames))}
	for id, frame := range state.frames {
		f := *frame
		f.rows, f.statements = slices.Clone(frame.rows), slices.Clone(frame.statements)
		f.producers = slices.Clone(frame.producers)
		f.before = maps.Clone(frame.before)
		copy.frames[id] = &f
	}
	return copy
}

func (s *StatementScope) restoreAfter(state *afterTriggerState) {
	if s.afterTriggers != nil && state != nil {
		for id, saved := range state.frames {
			if live := s.afterTriggers.frames[id]; live != nil {
				*live = *saved
				state.frames[id] = live
			}
		}
	}
	s.afterTriggers = state
}

func (f *afterTriggerFrame) cancelStatements(key afterStatementKey) {
	for i := range f.statements {
		event := &f.statements[i]
		if event.definition.Parts().Relation == key.relation && event.event == key.event {
			event.qualified, event.skip = true, true
		}
	}
}

// A later producer replaces the prior qualification for its target/event.
// Retain the group's position and alphabetical order without duplicate firing.
func (f *afterTriggerFrame) registerStatement(key afterStatementKey, event afterTriggerEvent) {
	at := len(f.statements)
	for i, prior := range f.statements {
		if prior.definition.Parts().Relation != key.relation || prior.event != key.event {
			continue
		}
		if prior.definition.OID() == event.definition.OID() {
			f.statements[i] = event
			return
		}
		if prior.definition.Name() > event.definition.Name() {
			at = i
			break
		}
		at = i + 1
	}
	f.statements = slices.Insert(f.statements, at, event)
}
