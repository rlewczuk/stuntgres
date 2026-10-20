package engine

import (
	"fmt"

	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
)

func (b *expressionBinder) captureSQLRoutineReference(path viewLiteralPath, n plan.SQLRoutineExpr) error {
	if err := n.ValidateStoredProvider(); err != nil {
		return err
	}
	read := b.BindingCatalog()
	if n.Ref != b.Host.CatalogOwner().Domain.Ref(read.Epoch(), n.Procedure.Address()) {
		return fmt.Errorf("stuntgres: invalid frozen SQL routine reference")
	}
	if _, err := routine.ValidateStoredRoutineDescriptor(read, n.Procedure); err != nil {
		return err
	}
	return b.Capture.CaptureRoutineReference(path, n.Ref)
}
