package engine

import (
	"errors"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Each lexical occurrence keeps its own capture even when physical grants
// deduplicate. Capture precedes the child's first AX wait; no later lookup may
// select a replacement endpoint after a wait or an earlier list action.
type inheritanceRelationshipAdmission struct {
	action   parser.AlterTableAction
	ref      ids.ObjectRef
	lexical  ids.ObjectRef
	relation *catalog.Relation
	deferred error
	admitted bool
}

func (s *StatementScope) captureInheritanceRelationshipsLocked(e *Engine, auth AuthorizationView, raw *parser.AlterTable, child ids.ObjectRef) error {
	if !table.HasRelationshipActions(raw.Actions) || child.Domain() == nil {
		return nil
	}
	auth.definition = nil // Observe actual names, not an ALTER recipe's reader.
	for _, action := range raw.Actions {
		if !table.RelationshipAction(action.Action) {
			continue
		}
		if action.Action == parser.InheritTable {
			if err := table.RelationshipChildKind(auth.Catalog.MustRelation(child.Address), action.Action); err != nil {
				return err
			}
		}
		ref, lookupErr := e.resolveRelationReference(auth, action.Parent, true)
		lexical, lexicalErr := e.admissionNameReference(auth, action.Parent)
		for _, err := range [...]error{lookupErr, lexicalErr} {
			var diagnostic *sqlstate.Error
			if err != nil && !errors.As(err, &diagnostic) {
				return err
			}
		}
		capture := inheritanceRelationshipAdmission{action: action, ref: ref, lexical: lexical, deferred: lookupErr}
		if capture.deferred == nil {
			capture.deferred = lexicalErr
		}
		if capture.ref.Domain() == nil {
			capture.ref = lexical
		}
		if capture.ref.Domain() != nil {
			capture.relation, _ = auth.Catalog.Relation(capture.ref.Address)
		}
		s.inheritance.relationships = append(s.inheritance.relationships, capture)
	}
	return nil
}

// Resolve semantics only when this misc action is reached. Initial absence is
// still an observation: a newly visible same-name object is never its target.
// Caller holds Engine.mu; neither lookup nor this comparison waits for grants.
func (s *StatementScope) currentInheritanceRelationshipLocked(e *Engine, capture *inheritanceRelationshipAdmission) (AuthorizationView, *catalog.Relation, error) {
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		return auth, nil, err
	}
	auth.definition = nil // Stage.Base must not hide a newly shadowing endpoint.
	ref, lookupErr := e.resolveRelationReference(auth, capture.action.Parent, true)
	lexical, lexicalErr := e.admissionNameReference(auth, capture.action.Parent)
	for _, err := range [...]error{lookupErr, lexicalErr} {
		var diagnostic *sqlstate.Error
		if err != nil && !errors.As(err, &diagnostic) {
			return auth, nil, err
		}
	}
	if lexicalErr == nil && lexical != capture.lexical {
		return auth, nil, ids.ErrCatalogReplaced
	}
	if capture.deferred != nil {
		if lookupErr != nil {
			return auth, nil, lookupErr
		}
		if lexicalErr != nil {
			return auth, nil, lexicalErr
		}
		return auth, nil, ids.ErrCatalogReplaced
	}
	actual, exists := auth.Catalog.Relation(capture.ref.Address)
	if lookupErr != nil || lexicalErr != nil || ref != capture.ref || !exists || capture.ref.Domain() != e.catalog.Domain || capture.ref.Epoch != auth.Catalog.Epoch() || !table.InheritanceMarkerPublication(capture.relation, actual) {
		return auth, nil, ids.ErrCatalogReplaced
	}
	return auth, actual, nil
}

func (s *StatementScope) admitInheritanceRelationship(capture *inheritanceRelationshipAdmission) error {
	if capture.admitted {
		return nil
	}
	if err := s.op.Context().Err(); err != nil {
		return err
	}
	e := s.engine()
	e.mu.Lock()
	_, _, err := s.currentInheritanceRelationshipLocked(e, capture)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	mode := lock.AccessShare
	if capture.action.Action == parser.InheritTable {
		mode = lock.ShareUpdateExclusive
	}
	if err := s.acquireSequence(capture.ref, mode, false); err != nil {
		return err
	}
	e.mu.Lock()
	auth, actual, err := s.currentInheritanceRelationshipLocked(e, capture)
	if err == nil && capture.action.Action == parser.InheritTable {
		err = auth.Check(access.Requirement{Object: capture.ref, OwnerOnly: true})
	}
	if err == nil {
		capture.relation, capture.admitted = actual, true
	}
	e.mu.Unlock()
	return err
}

func (s *StatementScope) validateInheritanceRelationshipsLocked(e *Engine) error {
	var auth AuthorizationView
	var err error
	captured := false
	for _, capture := range s.inheritance.relationships {
		if !capture.admitted {
			continue
		}
		if !captured {
			auth, err = e.authorizationLocked(s.op)
			if err != nil {
				return err
			}
			auth.definition = nil
			captured = true
		}
		current, ok := auth.Catalog.Relation(capture.ref.Address)
		lexical, err := e.admissionNameReference(auth, capture.action.Parent)
		if !ok || capture.ref.Domain() != e.catalog.Domain || capture.ref.Epoch != auth.Catalog.Epoch() || !table.InheritanceMarkerPublication(capture.relation, current) || err != nil || lexical != capture.lexical {
			return ids.ErrCatalogReplaced
		}
		if capture.action.Action == parser.InheritTable {
			if err := auth.Check(access.Requirement{Object: capture.ref, OwnerOnly: true}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *StatementScope) inheritanceRelationshipCandidate(stage *CatalogStage, child *catalog.Relation, _ int, action parser.AlterTableAction) (*catalog.Relation, error) {
	if err := table.RelationshipChildKind(child, action.Action); err != nil {
		return nil, err
	}
	for i := range s.inheritance.relationships {
		capture := &s.inheritance.relationships[i]
		if capture.action.Pos != action.Pos || capture.action.Action != action.Action {
			continue
		}
		if err := s.admitInheritanceRelationship(capture); err != nil {
			return nil, err
		}
		view := s.engine().stageRead(stage)
		parent, exists := view.Relation(capture.ref.Address)
		if !exists {
			return nil, ids.ErrCatalogReplaced
		}
		if _, staged := stage.Find(capture.ref.Address); !staged && parent != capture.relation && table.InheritanceMarkerPublication(parent, capture.relation) {
			// Consume the genuine intervening marker, rather than issuing a
			// second first-child marker from the stage's older base snapshot.
			if err := stage.Replace(capture.relation); err != nil {
				return nil, err
			}
			view = s.engine().stageRead(stage)
			parent = view.MustRelation(capture.ref.Address)
		}
		if action.Action == parser.InheritTable {
			return table.AttachInheritance(s.op.Context(), view, child, parent)
		}
		return table.DetachInheritance(view, child, parent, capture.ref)
	}
	return nil, ids.ErrCatalogReplaced
}
