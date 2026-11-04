package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
)

// SepgsqlPostAlter is called only by an actual supported semantic post-alter
// owner, never by generic descriptor COW. The signed attribute number is the
// released hook target, including the index-storage propagation source quirk.
// Like its stage, it supports both locked and detached producers; it takes no
// engine mutex and refreshes the physical MAC phase through sepgsqlView.
func (s *CatalogStage) SepgsqlPostAlter(relation *catalog.Relation, address ids.ObjectAddress, internal bool) error {
	if s.sepgsqlAuth.Sepgsql.Policy == nil || s.terminalCleanup || internal && address.SubID == 0 {
		return nil
	}
	auth, err := s.sepgsqlView()
	if err != nil {
		return err
	}
	parent := address
	parent.SubID = 0
	raw, err := auth.Object(auth.Ref(parent))
	if err != nil {
		return err
	}
	current, ok := raw.(*catalog.Relation)
	if !ok || relation == nil || relation.Address() != parent || current.RelationKind != relation.RelationKind {
		return ids.ErrCatalogReplaced
	}
	if address.SubID != 0 {
		s.ObserveSecurityLabel(catalog.SecurityLabelKey{Object: address, Provider: "selinux"})
		return auth.SepgsqlAttributePostAlter(relation, address)
	}
	if relation.RelationKind == catalog.IndexRelation {
		if relation.SQLIndex == nil {
			return ids.ErrCatalogReplaced
		}
		indexed, ok := auth.Catalog.Relation(relation.SQLIndex.Parent)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		if indexed.Partition != nil {
			return nil
		}
		return s.sepgsqlCheck(auth, indexed.Address(), sepgsql.Setattr)
	}
	if catalog.SepgsqlClass(relation, 0) == "" {
		return nil
	}
	s.ObserveSecurityLabel(catalog.SecurityLabelKey{Object: address, Provider: "selinux"})
	return auth.SepgsqlRelationPostAlter(relation)
}

// SepgsqlPostCreateColumn belongs to the actual new-slot owner; an inherited
// merge or unchanged descriptor does not manufacture a column-create event.
func (s *CatalogStage) SepgsqlPostCreateColumn(relation *catalog.Relation, sub int32) error {
	if s.sepgsqlAuth.Sepgsql.Policy == nil || s.terminalCleanup {
		return nil
	}
	auth, err := s.sepgsqlView()
	if err != nil {
		return err
	}
	if relation == nil || sub <= 0 || catalog.SepgsqlClass(relation, sub) != "db_column" {
		return nil
	}
	parent, err := auth.Object(auth.Ref(relation.Address()))
	if err != nil {
		return err
	}
	current, ok := parent.(*catalog.Relation)
	if !ok || current.RelationKind != relation.RelationKind {
		return ids.ErrCatalogReplaced
	}
	s.ObserveSecurityLabel(catalog.SecurityLabelKey{Object: relation.Address(), Provider: "selinux"})
	if err = auth.SepgsqlLabelBasis(relation.Address()); err != nil {
		return err
	}
	return s.sepgsqlCreateColumn(auth, relation, sub, auth.SepgsqlContext(relation.Address()))
}

// SepgsqlRelationNameChange belongs only to actual relation rename/move owners.
// An index post-alter projects to its parent and returns before schema checks.
func (s *CatalogStage) SepgsqlRelationNameChange(previous, next *catalog.Relation) error {
	if s.sepgsqlAuth.Sepgsql.Policy == nil || s.terminalCleanup {
		return nil
	}
	if previous == nil || next == nil || previous.Address() != next.Address() {
		return ids.ErrCatalogReplaced
	}
	if next.RelationKind == catalog.IndexRelation || catalog.SepgsqlClass(next, 0) == "" {
		return s.SepgsqlPostAlter(next, next.Address(), false)
	}
	auth, err := s.sepgsqlView()
	if err != nil {
		return err
	}
	if previous.Namespace() != next.Namespace() {
		if err = s.sepgsqlCheck(auth, previous.Namespace(), sepgsql.Permission("db_schema", "remove_name")); err != nil {
			return err
		}
		if err = s.sepgsqlCheck(auth, next.Namespace(), sepgsql.Permission("db_schema", "add_name")); err != nil {
			return err
		}
	}
	if previous.Name() != next.Name() {
		if err = s.sepgsqlCheck(auth, previous.Namespace(), sepgsql.Permission("db_schema", "remove_name")|sepgsql.Permission("db_schema", "add_name")); err != nil {
			return err
		}
	}
	return s.SepgsqlPostAlter(next, next.Address(), false)
}
