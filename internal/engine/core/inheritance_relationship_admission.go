package core

import (
	"errors"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Each lexical occurrence keeps its own capture even when physical grants
// deduplicate. Capture precedes the child's first AX wait; no later lookup may
// select a replacement endpoint after a wait or an earlier list action.
type InheritanceRelationshipAdmission struct {
	Action   parser.AlterTableAction
	Ref      ids.ObjectRef
	Lexical  ids.ObjectRef
	Relation *catalog.Relation
	Deferred error
	Admitted bool
}

func (s *StatementScope) captureInheritanceRelationshipsLocked(e *Engine, auth AuthorizationView, raw *parser.AlterTable, child ids.ObjectRef) error {
	if !table.HasRelationshipActions(raw.Actions) || child.Domain() == nil {
		return nil
	}
	auth.Definition = nil // Observe actual names, not an ALTER recipe's reader.
	for _, action := range raw.Actions {
		if !table.RelationshipAction(action.Action) {
			continue
		}
		if action.Action == parser.InheritTable {
			if err := table.RelationshipChildKind(auth.Catalog.MustRelation(child.Address), action.Action); err != nil {
				return err
			}
		}
		ref, lookupErr := e.ResolveRelationReference(auth, action.Parent, true)
		lexical, lexicalErr := e.AdmissionNameReference(auth, action.Parent)
		for _, err := range [...]error{lookupErr, lexicalErr} {
			var diagnostic *sqlstate.Error
			if err != nil && !errors.As(err, &diagnostic) {
				return err
			}
		}
		capture := InheritanceRelationshipAdmission{Action: action, Ref: ref, Lexical: lexical, Deferred: lookupErr}
		if capture.Deferred == nil {
			capture.Deferred = lexicalErr
		}
		if capture.Ref.Domain() == nil {
			capture.Ref = lexical
		}
		if capture.Ref.Domain() != nil {
			capture.Relation, _ = auth.Catalog.Relation(capture.Ref.Address)
		}
		s.Inheritance.Relationships = append(s.Inheritance.Relationships, capture)
	}
	return nil
}

func (s *StatementScope) validateInheritanceRelationshipsLocked(e *Engine) error {
	var auth AuthorizationView
	var err error
	captured := false
	for _, capture := range s.Inheritance.Relationships {
		if !capture.Admitted {
			continue
		}
		if !captured {
			auth, err = e.AuthorizationLocked(s.Op)
			if err != nil {
				return err
			}
			auth.Definition = nil
			captured = true
		}
		current, ok := auth.Catalog.Relation(capture.Ref.Address)
		lexical, err := e.AdmissionNameReference(auth, capture.Action.Parent)
		if !ok || capture.Ref.Domain() != e.Catalog.Domain || capture.Ref.Epoch != auth.Catalog.Epoch() || !table.InheritanceMarkerPublication(capture.Relation, current) || err != nil || lexical != capture.Lexical {
			return ids.ErrCatalogReplaced
		}
		if capture.Action.Action == parser.InheritTable {
			if err := auth.Check(access.Requirement{Object: capture.Ref, OwnerOnly: true}); err != nil {
				return err
			}
		}
	}
	return nil
}
