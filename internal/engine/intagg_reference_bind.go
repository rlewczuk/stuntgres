package engine

import (
	"stuntgres/internal/engine/extension"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Occurrences use the same normalized structural paths as frozen literal and
// column presentation. The captured reference comes from binding, never a later
// lookup by the written name or the renderer's current search_path.
func (b *expressionBinder) captureIntaggReference(path viewLiteralPath, proc *catalog.Procedure, ref ids.ObjectRef, name string) error {
	if err := extension.ValidateIntaggIdentity(b.bindingCatalog(), proc, ref, name); err != nil {
		return err
	}
	return b.capture.CaptureRoutineReference(path, ref)
}
