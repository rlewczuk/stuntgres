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
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
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
	segments         map[afterStatementKey]*transitionSegment
}

type afterStatementKey struct {
	relation ids.ObjectRef
	event    stored.TriggerEvent
}

// Candidate lists are immutable once a successful eager producer retains them.
type afterStatementProducer struct {
	key        afterStatementKey
	candidates []afterTriggerEvent
	segment    *transitionSegment
}

type afterTriggerEvent struct {
	definition stored.Trigger
	relation   *catalog.Relation
	read       catalog.Read
	event      stored.TriggerEvent
	old, next  []sqltype.Value
	targets    []int32            // Owned immutable stable UPDATE attributes.
	actor      *AuthorizationView // Immutable RI authority; live grants are recaptured.
	qualified  bool               // Statement WHEN is decided once at producer completion.
	skip       bool
	segment    *transitionSegment
}

func (s *StatementScope) afterFrame() *afterTriggerFrame {
	if s.afterTriggers == nil {
		s.afterTriggers = &afterTriggerState{frames: make(map[uint64]*afterTriggerFrame)}
	}
	if s.afterTriggers.frames == nil {
		s.afterTriggers.frames = make(map[uint64]*afterTriggerFrame)
	}
	f := s.afterTriggers.frames[s.effectCommand]
	if f == nil {
		f = &afterTriggerFrame{}
		s.afterTriggers.frames[s.effectCommand] = f
	}
	return f
}

func (h *statementHost) AfterRow(request dml.BeforeRow) error {
	return h.queueAfter(request.Relation, stored.TriggerEvent(request.Event), stored.TriggerRow, request.Targets, request.Old, request.New, false)
}

// Only an explicitly registered logical producer demands statement images.
// A definition on an unnamed physical leaf alone is not a capture consumer.
func (s *StatementScope) needsAfterRow(ref ids.ObjectRef, relation *catalog.Relation, event stored.TriggerEvent) bool {
	return dml.HasRowTriggers(relation, event, stored.TriggerAfter) ||
		s.registeredTransition(afterStatementKey{ref, event}) != nil
}

// AfterTransition records the named target's accepted images without inventing
// a physical ROW event (including the two separate halves of a moved UPDATE).
func (h *statementHost) AfterTransition(request dml.BeforeRow) error {
	return h.queueAfter(request.Relation, stored.TriggerEvent(request.Event), stored.TriggerRow, request.Targets, request.Old, request.New, true)
}

func (h *statementHost) AfterStatement(ref ids.ObjectRef, event stored.TriggerEvent, targets []int32) error {
	return h.queueAfter(ref, event, stored.TriggerStatement, targets, nil, nil, false)
}

func (h *statementHost) CompleteAfterStatement(ref ids.ObjectRef, event stored.TriggerEvent) error {
	s := h.scope()
	key := afterStatementKey{ref, event}
	if s.deferAfterProducers && s.deferredAfterCommand == s.effectCommand {
		if s.afterTriggers == nil || s.afterTriggers.frames[s.effectCommand] == nil {
			return nil
		}
		f := s.afterTriggers.frames[s.effectCommand]
		producer := afterStatementProducer{key: key, segment: f.segments[key]}
		for _, candidate := range f.statements {
			if !candidate.qualified && candidate.definition.Parts().Relation == ref && candidate.event == event && candidate.segment == producer.segment {
				producer.candidates = append(producer.candidates, candidate)
			}
		}
		f.producers = append(f.producers, producer)
		return nil
	}
	return s.qualifyAfterStatementsFor(&key)
}

// ExposeAfterStatement is called only after an RI producer and its paired
// internal trigger scope have both completed. It never borrows that scope's actor.
func (h *statementHost) ExposeAfterStatement(ref ids.ObjectRef, event stored.TriggerEvent) error {
	return h.scope().exposeAfterTransitions(afterStatementKey{ref, event})
}

func (h *statementHost) queueAfter(ref ids.ObjectRef, event stored.TriggerEvent, level stored.TriggerLevel, targets []int32, old, next []sqltype.Value, captureOnly bool) error {
	s := h.scope()
	if err := s.Active(); err != nil {
		return err
	}
	e := s.Engine()
	if level == stored.TriggerRow && !captureOnly {
		if target, _ := e.Locks.ReplacementGrants(s.LockTicket, ref, nil); !target {
			return fmt.Errorf("stuntgres: trigger invocation requires the admitted physical write target")
		}
	}
	if captureOnly && !e.Locks.Holds(s.LockTicket, lock.Resource{ObjectRef: ref}, lock.RowExclusive) {
		return fmt.Errorf("stuntgres: transition capture requires the admitted named write target")
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

	// Collection belongs to the accepted event, not to an invocation or WHEN.
	// A statement-only transition definition still demands row ingestion.
	var segment *transitionSegment
	successor := false
	var wantOld, wantNext bool
	registered := s.registeredTransition(afterStatementKey{ref, event}) != nil
	for _, definition := range definitions {
		p := definition.Parts()
		if p.Timing == stored.TriggerAfter && (level == stored.TriggerStatement || p.Level == stored.TriggerRow || registered) && definition.Mode().Fires(auth.RuleReplica()) && definition.Fires(event, targets) {
			wantOld = wantOld || p.OldTable != ""
			wantNext = wantNext || p.NewTable != ""
		}
	}
	if wantOld || wantNext {
		frame := s.afterFrame()
		prior := frame.segments[afterStatementKey{ref, event}]
		segment = frame.transition(afterStatementKey{ref, event}, relation.Columns, wantOld, wantNext, level == stored.TriggerRow)
		successor = prior != nil && prior != segment
		if level == stored.TriggerStatement {
			segment.statement = true
		}
	}
	var f *afterTriggerFrame
	key := afterStatementKey{ref, event}
	var images afterRowImages
	var ownedTargets []int32
	targetsOwned := false
	if level == stored.TriggerRow && segment != nil {
		images.detach(old, next, segment.wantOld, segment.wantNext)
		segment.append(images.old, images.next)
		if successor {
			// Zero-match RI registrations retain the exposed set. Only its
			// first successor image creates another statement opportunity.
			for _, definition := range definitions {
				p := definition.Parts()
				if p.Timing != stored.TriggerAfter || p.Level != stored.TriggerStatement || !definition.Fires(event, targets) {
					continue
				}
				if !targetsOwned {
					ownedTargets, targetsOwned = slices.Clone(targets), true
				}
				queued := afterTriggerEvent{definition: definition, relation: relation, read: auth.Catalog, event: event, actor: s.ForeignActionAuth, segment: segment, targets: ownedTargets}
				s.afterFrame().registerStatement(key, queued)
			}
		}
	}
	if captureOnly {
		return nil
	}
	if level == stored.TriggerStatement && s.afterTriggers != nil {
		if prior := s.afterTriggers.frames[s.effectCommand]; prior != nil {
			prior.cancelStatements(key, segment)
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
			segment: segment,
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
			images.detach(old, next, true, true)
			queued.old, queued.next = images.old, images.next
		}
		if !targetsOwned {
			ownedTargets, targetsOwned = slices.Clone(targets), true
		}
		queued.targets = ownedTargets
		if level == stored.TriggerRow {
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
			Context: s.ActivateRuntime(s.executionContext()), Host: s.WriteHost(), Catalog: event.read,
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
	f.cancelStatements(producer.key, producer.segment)
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
	if event.segment != nil && (p.OldTable != "" || p.NewTable != "") {
		event.segment.frozen = true
	}
	restoreRelations := s.installTriggerRelations(p, event.segment)
	defer restoreRelations()
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
			if err == nil && catalog.NativeDMLTrigger(d) && d.Builtin == nil {
				err = (stored.ProcedureProvider{}).Validate(auth.Catalog, d)
			}
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
		result, err := s.invokeNativeTrigger(d, event.relation, p, event.event, event.next, event.old, event.targets)
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

// RI transition exposure and final query draining share the same timing decision.
// A constraint event can share an ordinary trigger's segment without owning its
// aliases; only the scalar accepted payload may outlive the query.
func (s *StatementScope) fireAfterTrigger(event afterTriggerEvent) error {
	if c := event.definition.Parts().Constraint; c.OID != 0 {
		modes := s.constraintModes()
		if modes.Deferred(c.OID, c.Deferrable, c.InitiallyDeferred) {
			payload := &index.UserTriggerEvent{
				Definition: event.definition, Relation: event.relation, Read: event.read,
				Event: event.event, Old: event.old, Next: event.next, Targets: event.targets,
			}
			if event.actor != nil {
				payload.Actor = event.actor.State.CurrentUser
			}
			scope := s.beginConstraintEvents()
			scope.AppendConstraintEvent(index.ConstraintEvent{Parent: event.definition.Parts().Relation, Constraint: c.OID, UserTrigger: payload})
			return nil
		}
		return s.fireConstraintTrigger(event)
	}
	return s.fireTrigger(event)
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
		}
		if event.skip {
			continue
		}
		if err := s.fireAfterTrigger(event); err != nil {
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
		f.cloneTransitions()
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

func (f *afterTriggerFrame) cancelStatements(key afterStatementKey, segment *transitionSegment) {
	for i := range f.statements {
		event := &f.statements[i]
		if event.definition.Parts().Relation == key.relation && event.event == key.event && (event.segment == nil || !event.segment.frozen && (segment == nil || event.segment == segment)) {
			event.qualified, event.skip = true, true
		}
	}
}

// A later producer replaces the prior qualification for its target/event.
// Retain the group's position and alphabetical order without duplicate firing.
func (f *afterTriggerFrame) registerStatement(key afterStatementKey, event afterTriggerEvent) {
	at := len(f.statements)
	for i, prior := range f.statements {
		if prior.definition.Parts().Relation != key.relation || prior.event != key.event || prior.segment != event.segment {
			continue
		}
		if prior.definition.OID() == event.definition.OID() {
			if event.segment != nil && event.segment.frozen {
				return
			}
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
