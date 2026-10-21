package analyze

import (
	"fmt"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
)

func (b *Binder) captureSQLRoutineReference(path ViewLiteralPath, n plan.SQLRoutineExpr) error {
	if err := n.ValidateStoredProvider(); err != nil {
		return err
	}
	read := b.BindingCatalog()
	if n.Ref != b.Host.CatalogOwner().Domain.Ref(read.Epoch(), n.Procedure.Address()) {
		return fmt.Errorf("stuntgres: invalid frozen SQL routine reference")
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(read, n.Procedure); err != nil {
		return err
	}
	return b.Capture.CaptureRoutineReference(path, n.Ref)
}
