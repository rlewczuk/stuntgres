package view

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
)

// RelationAdmission augments view execution's existing authority environment.
// Admission is metadata-only; implementations must not enter providers.
type RelationAdmission interface {
	access.Environment
	AdmitRelation(*catalog.Relation) error
}
