package engine

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"time"
)

// DISCARD retains physical attachments. Only TEMP uses the rollbackable catalog
// overlay; the other subsets are irreversible owner-local session effects.
func (s *StatementScope) executeDiscard(raw *parser.Discard, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if raw.Category > parser.DiscardTemp {
		return Result{}, fmt.Errorf("stuntgres: invalid DISCARD category")
	}
	if raw.Category == parser.DiscardAll && s.owner.inBlock() {
		return Result{}, sqlError("25001", "DISCARD ALL cannot run inside a transaction block")
	}
	session := s.op.session
	if raw.Category == parser.DiscardPlans || raw.Category == parser.DiscardAll {
		if session.discardPlansGeneration == ^uint64(0) {
			return Result{}, fmt.Errorf("stuntgres: discard plan generation exhausted")
		}
	}
	var reset authorization.RoleState
	if raw.Category == parser.DiscardAll {
		var err error
		reset, err = s.discardAllPreflight()
		if err != nil {
			return Result{}, err
		}
	}
	if raw.Category == parser.DiscardTemp || raw.Category == parser.DiscardAll {
		if err := s.stageDiscardTemporary(raw.Category == parser.DiscardAll); err != nil {
			return Result{}, err
		}
		if raw.Category == parser.DiscardTemp {
			return Result{}, nil
		}
	}
	if err := s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
	if raw.Category == parser.DiscardAll {
		e := session.Engine()
		e.mu.Lock()
		state := session.notifications.state
		history := state != nil && state.history
		e.mu.Unlock()
		if history {
			s.notificationActions = append(s.notificationActions, notificationAction{all: true})
		}
		s.op.discardRoleReset = reset
		defer func() { s.op.discardRoleReset = authorization.RoleState{} }()
		// Commit the validated TEMP closure with the normal combined publisher.
		// Admission and replacement authority stay held until Finish. After this
		// succeeds, no fallible owner operation or context check remains.
		if err := e.publishRows(s.op, s.txRef, s.stage.rows, s.stage.deletes, s.catalog, s.catalogAuthority, s); err != nil {
			return Result{}, err
		}
		e.mu.Lock()
		session.settings.mu.Lock()
		session.mu.Lock()
		e.resetAllSettingsLocked(session)
		session.mu.Unlock()
		session.settings.mu.Unlock()
		e.mu.Unlock()
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		if err := disposeSQLCursors(s.op.Context(), session.cursors.clear()); err != nil {
			return Result{}, err
		}
		clear(session.preparedSQL.entries)
		// The persistent ticket was validated before publication and cannot end
		// while this operation lease is live. Manager callbacks run unlocked.
		e.locks.discardAdvisoryHolds(session.advisory.ticket)
	}
	if raw.Category == parser.DiscardPlans || raw.Category == parser.DiscardAll {
		session.discardPlansGeneration++
	}
	if raw.Category == parser.DiscardSequences || raw.Category == parser.DiscardAll {
		e := session.Engine()
		e.mu.Lock()
		if raw.Category == parser.DiscardAll {
			clear(session.sequences) // The full plan-generation event survives.
		} else {
			for address, memory := range session.sequences {
				if memory.analysisStamp == 0 {
					delete(session.sequences, address)
				} else {
					// DISCARD SEQUENCES forgets values and unused tails, not
					// an unconsumed relation-source invalidation after rollback.
					*memory = sequenceMemory{analysisStamp: memory.analysisStamp}
				}
			}
		}
		session.lastSequence = ids.ObjectAddress{}
		e.mu.Unlock()
	}
	s.completed, s.sessionCompleted = true, true
	return Result{}, nil
}

func (s *StatementScope) discardAllPreflight() (authorization.RoleState, error) {
	session, e := s.op.session, s.op.session.Engine()
	ticket := session.advisory.ticket
	if !e.locks.validSession(ticket, session.token) {
		return authorization.RoleState{}, fmt.Errorf("stuntgres: invalid advisory session")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	session.settings.mu.Lock()
	defer session.settings.mu.Unlock()
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := publicationOperationLocked(e, s.op); err != nil {
		return authorization.RoleState{}, err
	}
	if session.settings.ended {
		return authorization.RoleState{}, fmt.Errorf("stuntgres: settings attachment ended")
	}
	if err := roleRevisionHeadroom(session.authorization, 3); err != nil {
		return authorization.RoleState{}, err
	}
	reset := session.authorization.state
	reset.SessionUser, reset.CurrentUser = reset.Authenticated, reset.Authenticated
	if reset.ResetRole != (authorization.PrincipalRef{}) {
		reset.CurrentUser = reset.ResetRole
	}
	roles := e.roleRead(CatalogRead{e, e.state}, e.roles)
	for _, principal := range []authorization.PrincipalRef{reset.Authenticated, reset.CurrentUser} {
		if _, err := roles.Descriptor(principal); err != nil {
			return authorization.RoleState{}, err
		}
	}
	return reset, nil
}

// The combined publisher calls these under Engine -> session locks, closing the
// gap between role-identity validation and irreversible TEMP publication.
func (e *Engine) discardResetPreflightLocked(op *Operation) error {
	if op.discardRoleReset.Authenticated == (authorization.PrincipalRef{}) {
		return nil
	}
	if err := roleRevisionHeadroom(op.session.authorization, 3); err != nil {
		return err
	}
	roles := e.roleRead(CatalogRead{e, e.state}, e.roles)
	for _, principal := range []authorization.PrincipalRef{op.discardRoleReset.Authenticated, op.discardRoleReset.CurrentUser} {
		if _, err := roles.Descriptor(principal); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) finishDiscardRoleResetLocked(op *Operation) {
	if op.discardRoleReset.Authenticated != (authorization.PrincipalRef{}) {
		op.session.authorization.state = op.discardRoleReset
		op.session.authorization.revision++
	}
}

// Caller holds Engine -> settings -> session locks. Defaults and placeholder
// recognition remain intact; the default provider is never invoked again.
func (e *Engine) resetAllSettingsLocked(session *Session) {
	owner := session.settings
	timeoutChanged, transactionAssignment := false, false
	for key := range owner.values {
		if d, ok := e.settingsDefinitions[key]; ok && d.Access != SettingSession && d.Name != "lc_messages" {
			continue
		}
		owner.touch(key, false)
		delete(owner.values, key)
		delete(owner.typed, key)
		switch key {
		case "transaction_timeout":
			timeoutChanged, transactionAssignment = true, true
		case "idle_in_transaction_session_timeout", "idle_session_timeout":
			timeoutChanged = true
		}
	}
	if timeoutChanged {
		session.publishTimeoutSettingsLocked(owner, transactionAssignment, time.Now())
	}
}

func (s *StatementScope) stageDiscardTemporary(closeCursors bool) error {
	e, session := s.op.session.Engine(), s.op.session
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	base := auth.catalog
	owned := make(map[ids.ObjectAddress]bool)
	for address, d := range base.state.descriptors {
		if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary == session.token {
			owned[address] = true
		}
	}
	children, err := namespaceChildren(base, owned)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	if len(children) == 0 {
		e.mu.Unlock()
		return nil
	}
	changes := cloneCatalogChanges(s.owner.catalog)
	stage := e.BeginCatalogStage(base)
	stage.authorizeLocked(auth, session)
	for address := range owned {
		stage.domainBases = append(stage.domainBases, base.state.descriptors[address].(*namespaceDescriptor))
	}
	// Exclude every owned target before projecting surviving dependents, exactly
	// as physical collation cleanup does, but retain the namespace descriptor.
	for _, address := range children {
		if err = stage.removeObjectLocked(e.domain.Ref(base.Epoch(), address), false); err != nil {
			break
		}
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return err
	}
	if err = s.stageExpressionDependents(stage, children, parser.DropCascade, changes); err != nil {
		return err
	}
	if err = s.stageOwnedSequences(stage, children, parser.DropCascade, changes); err != nil {
		return err
	}
	var relations []ids.ObjectAddress
	for address := range stage.removals {
		if address.ClassID == ids.RelationClassID {
			relations = append(relations, address)
		}
	}
	for _, address := range children {
		if address.ClassID == ids.RelationClassID {
			relations = append(relations, address)
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].ObjectID < relations[j].ObjectID })
	if !closeCursors {
		for i, address := range relations {
			if i != 0 && address == relations[i-1] {
				continue
			}
			if _, removed := stage.removals[address]; !removed {
				continue
			}
			if err := s.discardTemporaryCursorPreflight(stage, address); err != nil {
				return err
			}
		}
	}
	for i, address := range relations {
		if i != 0 && address == relations[i-1] {
			continue
		}
		if err = s.acquireSequence(e.domain.Ref(base.Epoch(), address), accessExclusive, true); err != nil {
			return err
		}
	}
	if _, err = e.ValidateCatalogStage(s.op.Context(), stage); err != nil {
		return err
	}
	if err = e.stageCatalogOverlay(s, stage, changes); err != nil {
		return err
	}
	s.owner.accessedTemporary = true
	return nil
}

// Both destructive utilities check the actual selected closure. Exhausted
// portals retain source pins; failed and exported HOLD roots do not.
func (s *StatementScope) discardTemporaryCursorPreflight(stage *CatalogStage, address ids.ObjectAddress) error {
	return s.relationCursorPreflight(stage.base, address, "DROP TABLE")
}

func (s *StatementScope) relationCursorPreflight(catalog CatalogRead, address ids.ObjectAddress, command string) error {
	session := s.op.session
	for _, cursor := range session.cursors.entries {
		if cursor.tx != s.txRef || cursor.root == nil || cursor.position == cursorFailed {
			continue
		}
		for _, leaf := range cursor.leaves {
			if leaf.ref.Domain() != catalog.owner.domain || leaf.ref.Epoch != catalog.Epoch() || leaf.ref.Address != address {
				continue
			}
			relation, ok := catalog.state.descriptors[address].(*relationDescriptor)
			if !ok || leaf.relation == nil {
				continue
			}
			return sqlError("55006", fmt.Sprintf("cannot %s \"%s\" because it is being used by active queries in this session", command, relation.name))
		}
	}
	return nil
}
