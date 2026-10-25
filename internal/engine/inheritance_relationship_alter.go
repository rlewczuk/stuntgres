package engine

import (
	"errors"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Resolve semantics only when this misc action is reached. Initial absence is
// still an observation: a newly visible same-name object is never its target.
// Caller holds Engine.mu; neither lookup nor this comparison waits for grants.
func currentInheritanceRelationshipLocked(s *core.StatementScope, e *core.Engine, capture *core.InheritanceRelationshipAdmission) (core.AuthorizationView, *catalog.Relation, error) {
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return auth, nil, err
	}
	auth.Definition = nil // Stage.Base must not hide a newly shadowing endpoint.
	ref, lookupErr := e.ResolveRelationReference(auth, capture.Action.Parent, true)
	lexical, lexicalErr := e.AdmissionNameReference(auth, capture.Action.Parent)
	for _, err := range [...]error{lookupErr, lexicalErr} {
		var diagnostic *sqlstate.Error
		if err != nil && !errors.As(err, &diagnostic) {
			return auth, nil, err
		}
	}
	if lexicalErr == nil && lexical != capture.Lexical {
		return auth, nil, ids.ErrCatalogReplaced
	}
	if capture.Deferred != nil {
		if lookupErr != nil {
			return auth, nil, lookupErr
		}
		if lexicalErr != nil {
			return auth, nil, lexicalErr
		}
		return auth, nil, ids.ErrCatalogReplaced
	}
	actual, exists := auth.Catalog.Relation(capture.Ref.Address)
	if lookupErr != nil || lexicalErr != nil || ref != capture.Ref || !exists || capture.Ref.Domain() != e.Catalog.Domain || capture.Ref.Epoch != auth.Catalog.Epoch() || !table.InheritanceMarkerPublication(capture.Relation, actual) {
		return auth, nil, ids.ErrCatalogReplaced
	}
	return auth, actual, nil
}

func admitInheritanceRelationship(s *core.StatementScope, capture *core.InheritanceRelationshipAdmission) error {
	if capture.Admitted {
		return nil
	}
	if err := s.Op.Context().Err(); err != nil {
		return err
	}
	e := s.Engine()
	e.Mu.Lock()
	_, _, err := currentInheritanceRelationshipLocked(s, e, capture)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	mode := lock.AccessShare
	if capture.Action.Action == parser.InheritTable {
		mode = lock.ShareUpdateExclusive
	}
	if err := s.AcquireSequence(capture.Ref, mode, false); err != nil {
		return err
	}
	e.Mu.Lock()
	auth, actual, err := currentInheritanceRelationshipLocked(s, e, capture)
	if err == nil && capture.Action.Action == parser.InheritTable {
		err = auth.Check(access.Requirement{Object: capture.Ref, OwnerOnly: true})
	}
	if err == nil {
		capture.Relation, capture.Admitted = actual, true
	}
	e.Mu.Unlock()
	return err
}

func inheritanceRelationshipCandidate(s *core.StatementScope, stage *core.CatalogStage, child *catalog.Relation, _ int, action parser.AlterTableAction) (*catalog.Relation, error) {
	if err := table.RelationshipChildKind(child, action.Action); err != nil {
		return nil, err
	}
	for i := range s.Inheritance.Relationships {
		capture := &s.Inheritance.Relationships[i]
		if capture.Action.Pos != action.Pos || capture.Action.Action != action.Action {
			continue
		}
		if err := admitInheritanceRelationship(s, capture); err != nil {
			return nil, err
		}
		view := s.Engine().StageRead(stage)
		parent, exists := view.Relation(capture.Ref.Address)
		if !exists {
			return nil, ids.ErrCatalogReplaced
		}
		if _, staged := stage.Find(capture.Ref.Address); !staged && parent != capture.Relation && table.InheritanceMarkerPublication(parent, capture.Relation) {
			// Consume the genuine intervening marker, rather than issuing a
			// second first-child marker from the stage's older base snapshot.
			if err := stage.Replace(capture.Relation); err != nil {
				return nil, err
			}
			view = s.Engine().StageRead(stage)
			parent = view.MustRelation(capture.Ref.Address)
		}
		if action.Action == parser.InheritTable {
			return table.AttachInheritance(s.Op.Context(), view, child, parent)
		}
		return table.DetachInheritance(view, child, parent, capture.Ref)
	}
	return nil, ids.ErrCatalogReplaced
}
