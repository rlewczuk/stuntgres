package engine

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/settings"
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
	if raw.Category == parser.DiscardAll && s.owner.InBlock() {
		return Result{}, sqlError("25001", "DISCARD ALL cannot run inside a transaction block")
	}
	session := s.op.Session
	if raw.Category == parser.DiscardPlans || raw.Category == parser.DiscardAll {
		if session.DiscardPlansGeneration == ^uint64(0) {
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
		e := engineOf(session)
		e.mu.Lock()
		state := payloadOf(session).notifications.state
		history := state != nil && state.history
		e.mu.Unlock()
		if history {
			s.notificationActions = append(s.notificationActions, notificationAction{All: true})
		}
		s.op.DiscardRoleReset = reset
		defer func() { s.op.DiscardRoleReset = authorization.RoleState{} }()
		// Commit the validated TEMP closure with the normal combined publisher.
		// Admission and replacement authority stay held until Finish. After this
		// succeeds, no fallible owner operation or context check remains.
		if err := e.publishRows(s.op, s.txRef, s.stage.rows, s.stage.deletes, s.catalog, s.catalogAuthority, s); err != nil {
			return Result{}, err
		}
		e.mu.Lock()
		session.Settings.Mu.Lock()
		session.Mu.Lock()
		e.resetAllSettingsLocked(session)
		session.Mu.Unlock()
		session.Settings.Mu.Unlock()
		e.mu.Unlock()
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		if err := disposeSQLCursors(s.op.Context(), payloadOf(session).cursors.clear()); err != nil {
			return Result{}, err
		}
		clear(payloadOf(session).preparedSQL.entries)
		// The persistent ticket was validated before publication and cannot end
		// while this operation lease is live. Manager callbacks run unlocked.
		e.locks.DiscardAdvisoryHolds(session.Advisory.Ticket)
	}
	if raw.Category == parser.DiscardPlans || raw.Category == parser.DiscardAll {
		session.DiscardPlansGeneration++
		e := engineOf(session)
		e.mu.Lock()
		payloadOf(session).sqlRoutines = nil
		e.mu.Unlock()
	}
	if raw.Category == parser.DiscardSequences || raw.Category == parser.DiscardAll {
		e := engineOf(session)
		e.mu.Lock()
		payloadOf(session).sequences.DiscardLocked(raw.Category == parser.DiscardAll)
		e.mu.Unlock()
	}
	s.completed, s.sessionCompleted = true, true
	return Result{}, nil
}

func (s *StatementScope) discardAllPreflight() (authorization.RoleState, error) {
	session, e := s.op.Session, s.engine()
	ticket := session.Advisory.Ticket
	if !e.locks.ValidSession(ticket, session.SessionToken) {
		return authorization.RoleState{}, fmt.Errorf("stuntgres: invalid advisory session")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	session.Settings.Mu.Lock()
	defer session.Settings.Mu.Unlock()
	session.Mu.Lock()
	defer session.Mu.Unlock()
	if err := publicationOperationLocked(e, s.op); err != nil {
		return authorization.RoleState{}, err
	}
	if session.Settings.Ended {
		return authorization.RoleState{}, fmt.Errorf("stuntgres: settings attachment ended")
	}
	if err := roleRevisionHeadroom(session.Authorization, 3); err != nil {
		return authorization.RoleState{}, err
	}
	reset := session.Authorization.State
	reset.SessionUser, reset.CurrentUser = reset.Authenticated, reset.Authenticated
	if reset.ResetRole != (authorization.PrincipalRef{}) {
		reset.CurrentUser = reset.ResetRole
	}
	roles := e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published)
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
	if op.DiscardRoleReset.Authenticated == (authorization.PrincipalRef{}) {
		return nil
	}
	if err := roleRevisionHeadroom(op.Session.Authorization, 3); err != nil {
		return err
	}
	roles := e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published)
	for _, principal := range []authorization.PrincipalRef{op.DiscardRoleReset.Authenticated, op.DiscardRoleReset.CurrentUser} {
		if _, err := roles.Descriptor(principal); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) finishDiscardRoleResetLocked(op *Operation) {
	if op.DiscardRoleReset.Authenticated != (authorization.PrincipalRef{}) {
		op.Session.Authorization.State = op.DiscardRoleReset
		op.Session.Authorization.Revision++
	}
}

// Caller holds Engine -> settings -> session locks. Defaults and placeholder
// recognition remain intact; the default provider is never invoked again.
func (e *Engine) resetAllSettingsLocked(session *Session) {
	owner := session.Settings
	timeoutChanged, transactionAssignment := false, false
	for key := range owner.Values {
		if d, ok := e.settings.Definitions()[key]; ok && d.Access != settings.Session && d.Name != "lc_messages" {
			continue
		}
		owner.Touch(key, false)
		delete(owner.Values, key)
		delete(owner.Typed, key)
		switch key {
		case "transaction_timeout":
			timeoutChanged, transactionAssignment = true, true
		case "idle_in_transaction_session_timeout", "idle_session_timeout":
			timeoutChanged = true
		}
	}
	if timeoutChanged {
		session.PublishTimeoutSettingsLocked(owner, transactionAssignment, time.Now())
	}
}

func (s *StatementScope) stageDiscardTemporary(closeCursors bool) error {
	e, session := s.engine(), s.op.Session
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	base := auth.Catalog
	owned := make(map[ids.ObjectAddress]bool)
	for address, d := range base.Descriptors() {
		if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary == session.SessionToken {
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
	changes := s.owner.Catalog.Clone()
	stage := e.BeginCatalogStage(base)
	stage.authorizeLocked(auth, session)
	for address := range owned {
		stage.domainBases = append(stage.domainBases, base.MustNamespace(address))
	}
	// Exclude every owned target before projecting surviving dependents, exactly
	// as physical collation cleanup does, but retain the namespace descriptor.
	for _, address := range children {
		if err = stage.removeObjectLocked(e.catalog.Domain.Ref(base.Epoch(), address), false); err != nil {
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
	for address := range stage.Removals() {
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
			if _, removed := stage.Removal(address); !removed {
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
		if err = s.acquireSequence(e.catalog.Domain.Ref(base.Epoch(), address), lock.AccessExclusive, true); err != nil {
			return err
		}
	}
	if _, err = e.ValidateCatalogStage(s.op.Context(), stage); err != nil {
		return err
	}
	if err = e.stageCatalogOverlay(s, stage, changes); err != nil {
		return err
	}
	s.owner.AccessedTemporary = true
	return nil
}

// Both destructive utilities check the actual selected closure. Exhausted
// portals retain source pins; failed and exported HOLD roots do not.
func (s *StatementScope) discardTemporaryCursorPreflight(stage *CatalogStage, address ids.ObjectAddress) error {
	return s.relationCursorPreflight(stage.Base, address, "DROP TABLE")
}

func (s *StatementScope) relationCursorPreflight(catalog catalog.Read, address ids.ObjectAddress, command string) error {
	session := s.op.Session
	for _, cursor := range payloadOf(session).cursors.entries {
		if cursor.tx != s.txRef || cursor.root == nil || cursor.position == cursorFailed {
			continue
		}
		for _, leaf := range cursor.leaves {
			if leaf.Ref.Domain() != catalog.Owner().Domain || leaf.Ref.Epoch != catalog.Epoch() || leaf.Ref.Address != address {
				continue
			}
			relation, ok := catalog.Relation(address)
			if !ok || leaf.Relation == nil {
				continue
			}
			return sqlError("55006", fmt.Sprintf("cannot %s \"%s\" because it is being used by active queries in this session", command, relation.Name()))
		}
	}
	return nil
}
