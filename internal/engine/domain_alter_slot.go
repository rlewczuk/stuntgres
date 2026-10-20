package engine

import (
	"context"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// domainAlterStage is the ALTER DOMAIN state of one catalog stage. A nil slot
// is a stage that alters no domain.
type DomainAlterStage struct {
	Ctx        context.Context // the altering operation; nil outside ALTER DOMAIN
	Validation *DomainValidationFence
	IndexRoots map[ids.ObjectAddress]*storage.IndexRoot
}

func (d *DomainAlterStage) AlterContext() context.Context {
	if d == nil {
		return nil
	}
	return d.Ctx
}

func (d *DomainAlterStage) ValidationFence() *DomainValidationFence {
	if d == nil {
		return nil
	}
	return d.Validation
}

func (d *DomainAlterStage) AlteredIndexRoots() map[ids.ObjectAddress]*storage.IndexRoot {
	if d == nil {
		return nil
	}
	return d.IndexRoots
}
