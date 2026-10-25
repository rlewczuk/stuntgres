package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// The public regression proves real view/body execution and ACL transitions.
// Each slot must retain its own historical declaration, never another slot's
// declaration or a replacement from the current catalog.
type routineTestCapture struct {
	Ref       ids.ObjectRef
	Procedure *catalog.Procedure
}
