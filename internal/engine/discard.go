package engine

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
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
	if raw.Category == parser.DiscardAll && s.Owner.InBlock() {
		return Result{}, SQLError("25001", "DISCARD ALL cannot run inside a transaction block")
	}
	sess := s.Op.Session
	if raw.Category == parser.DiscardPlans || raw.Category == parser.DiscardAll {
		if sess.DiscardPlansGeneration == ^uint64(0) {
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
		e := EngineOf(sess)
		e.Mu.Lock()
		state := PayloadOf(sess).notifications.State
		history := state != nil && state.History
		e.Mu.Unlock()
		if history {
			s.notificationActions = append(s.notificationActions, session.NotificationAction{All: true})
		}
		s.Op.DiscardRoleReset = reset
		defer func() { s.Op.DiscardRoleReset = authorization.RoleState{} }()
		// Commit the validated TEMP closure with the normal combined publisher.
		// Admission and replacement authority stay held until Finish. After this
		// succeeds, no fallible owner operation or context check remains.
		if err := e.publishRows(s.Op, s.TxRef, s.Stage.Rows, s.Stage.Deletes, s.Catalog, s.CatalogAuthority, s); err != nil {
			return Result{}, err
		}
		e.Mu.Lock()
		sess.Settings.Mu.Lock()
		sess.Mu.Lock()
		sess.Settings.ResetAllLocked(e.Settings)
		sess.Mu.Unlock()
		sess.Settings.Mu.Unlock()
		e.Mu.Unlock()
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		if err := disposeSQLCursors(s.Op.Context(), PayloadOf(sess).Cursors.clear()); err != nil {
			return Result{}, err
		}
		clear(PayloadOf(sess).preparedSQL.entries)
		// The persistent ticket was validated before publication and cannot end
		// while this operation lease is live. Manager callbacks run unlocked.
		e.Locks.DiscardAdvisoryHolds(sess.Advisory.Ticket)
	}
	if raw.Category == parser.DiscardPlans || raw.Category == parser.DiscardAll {
		sess.DiscardPlansGeneration++
		e := EngineOf(sess)
		e.Mu.Lock()
		PayloadOf(sess).sqlRoutines = nil
		e.Mu.Unlock()
	}
	if raw.Category == parser.DiscardSequences || raw.Category == parser.DiscardAll {
		e := EngineOf(sess)
		e.Mu.Lock()
		PayloadOf(sess).Sequences.DiscardLocked(raw.Category == parser.DiscardAll)
		e.Mu.Unlock()
	}
	s.Completed, s.sessionCompleted = true, true
	return Result{}, nil
}

func (s *StatementScope) discardAllPreflight() (authorization.RoleState, error) {
	sess, e := s.Op.Session, s.Engine()
	ticket := sess.Advisory.Ticket
	if !e.Locks.ValidSession(ticket, sess.SessionToken) {
		return authorization.RoleState{}, fmt.Errorf("stuntgres: invalid advisory session")
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	sess.Settings.Mu.Lock()
	defer sess.Settings.Mu.Unlock()
	sess.Mu.Lock()
	defer sess.Mu.Unlock()
	if err := sess.Settings.AdmitLocked(DomainOf(e), s.Op); err != nil {
		return authorization.RoleState{}, err
	}
	reset := session.DiscardRoleReset(sess.Authorization)
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current())
	if err := session.ValidateRoleResetLocked(sess.Authorization, reset, roles); err != nil {
		return authorization.RoleState{}, err
	}
	return reset, nil
}

func (s *StatementScope) stageDiscardTemporary(closeCursors bool) error {
	e, session := s.Engine(), s.Op.Session
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	base := auth.Catalog
	owned := make(map[ids.ObjectAddress]bool)
	for address, d := range base.Descriptors() {
		if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary == session.SessionToken {
			owned[address] = true
		}
	}
	children, err := catalog.NamespaceChildren(base, owned)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	if len(children) == 0 {
		e.Mu.Unlock()
		return nil
	}
	changes := s.Owner.Catalog.Clone()
	stage := e.BeginCatalogStage(base)
	stage.AuthorizeLocked(auth, session)
	for address := range owned {
		stage.DomainBases = append(stage.DomainBases, base.MustNamespace(address))
	}
	// Exclude every owned target before projecting surviving dependents, exactly
	// as physical collation cleanup does, but retain the namespace descriptor.
	for _, address := range children {
		if err = stage.RemoveObjectLocked(e.Catalog.Domain.Ref(base.Epoch(), address), false); err != nil {
			break
		}
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return err
	}
	if err = s.StageExpressionDependents(stage, children, parser.DropCascade, changes); err != nil {
		return err
	}
	if err = s.StageOwnedSequences(stage, children, parser.DropCascade, changes); err != nil {
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
		if err = s.AcquireSequence(e.Catalog.Domain.Ref(base.Epoch(), address), lock.AccessExclusive, true); err != nil {
			return err
		}
	}
	if _, err = e.ValidateCatalogStage(s.Op.Context(), stage); err != nil {
		return err
	}
	if err = e.StageCatalogOverlay(s, stage, changes); err != nil {
		return err
	}
	s.Owner.AccessedTemporary = true
	return nil
}

// Both destructive utilities check the actual selected closure. Exhausted
// portals retain source pins; failed and exported HOLD roots do not.
func (s *StatementScope) discardTemporaryCursorPreflight(stage *CatalogStage, address ids.ObjectAddress) error {
	return s.relationCursorPreflight(stage.Base, address, "DROP TABLE")
}

func (s *StatementScope) relationCursorPreflight(catalog catalog.Read, address ids.ObjectAddress, command string) error {
	session := s.Op.Session
	for _, cursor := range PayloadOf(session).Cursors.Entries {
		if cursor.tx != s.TxRef || cursor.root == nil || cursor.Place == exec.CursorFailed {
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
			return SQLError("55006", fmt.Sprintf("cannot %s \"%s\" because it is being used by active queries in this session", command, relation.Name()))
		}
	}
	return nil
}
