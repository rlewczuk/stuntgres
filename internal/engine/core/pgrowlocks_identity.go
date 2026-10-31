package core

import (
	"fmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

// ValidatePgrowlocks checks the installed pgrowlocks declaration against
// current authority.
func ValidatePgrowlocks[A Authority](h Host[A], n plan.InstalledPgrowlocksExpr) error {
	if err := h.Active(); err != nil {
		return err
	}
	h.ReacquireEngine()
	auth, err := h.WriteAuthorizationLocked()
	h.ReleaseEngineLocked()
	if err != nil {
		return err
	}
	current, ok := auth.Access().Catalog.Procedure(n.Ref.Address)
	if !ok || n.RoutineReference != n.Procedure || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return fmt.Errorf("stuntgres: stale installed pgrowlocks declaration")
	}
	n.Procedure, n.RoutineReference = current, current
	return stored.ValidateInstalledPgrowlocks(n, auth.Access().Catalog)
}
