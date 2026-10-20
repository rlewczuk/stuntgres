package engine

import (
	"context"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// domainAlterStage is the ALTER DOMAIN state of one catalog stage. A nil slot
// is a stage that alters no domain.
type domainAlterStage struct {
	ctx        context.Context // the altering operation; nil outside ALTER DOMAIN
	validation *domainValidationFence
	indexRoots map[ids.ObjectAddress]*storage.IndexRoot
}

// domainAlterState returns the stage's ALTER DOMAIN slot, allocating it on
// first use.
func (s *CatalogStage) domainAlterState() *domainAlterStage {
	if s.domainAlter == nil {
		s.domainAlter = new(domainAlterStage)
	}
	return s.domainAlter
}

func (d *domainAlterStage) alterContext() context.Context {
	if d == nil {
		return nil
	}
	return d.ctx
}

func (d *domainAlterStage) validationFence() *domainValidationFence {
	if d == nil {
		return nil
	}
	return d.validation
}

func (d *domainAlterStage) alteredIndexRoots() map[ids.ObjectAddress]*storage.IndexRoot {
	if d == nil {
		return nil
	}
	return d.indexRoots
}
