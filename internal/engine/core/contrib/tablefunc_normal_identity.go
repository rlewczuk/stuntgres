package contrib

import (
	"fmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

// ValidateTablefuncNormal checks the installed normal_rand declaration
// against current authority.
func ValidateTablefuncNormal[A Authority](h Host[A], n plan.InstalledTablefuncNormalExpr) error {
	if err := h.Active(); err != nil { return err }
	h.ReacquireEngine()
	auth, err := h.WriteAuthorizationLocked()
	h.ReleaseEngineLocked()
	if err != nil { return err }
	current, ok := auth.Access().Catalog.Procedure(n.Ref.Address)
	if !ok || n.RoutineReference != n.Procedure || !exec.SameRoutineDeclaration(current, n.Procedure) { return fmt.Errorf("stuntgres: stale installed normal_rand declaration") }
	n.Procedure, n.RoutineReference = current, current
	return stored.ValidateInstalledTablefuncNormal(n, auth.Access().Catalog)
}
