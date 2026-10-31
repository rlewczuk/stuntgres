package core

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

// ValidateVerifyHeapam checks the installed verify_heapam declaration against
// current authority.
func ValidateVerifyHeapam[A Authority](h Host[A], n plan.InstalledVerifyHeapamExpr) error {
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
		return fmt.Errorf("stuntgres: stale installed verify_heapam declaration")
	}
	n.Procedure, n.RoutineReference = current, current
	if err := stored.ValidateInstalledVerifyHeapam(n, auth.Access().Catalog); err != nil {
		return err
	}
	return auth.Access().Check(auth, access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege})
}
