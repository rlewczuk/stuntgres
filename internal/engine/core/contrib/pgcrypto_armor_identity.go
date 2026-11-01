package contrib

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

// ValidatePgpArmorHeaders checks the installed pgp_armor_headers declaration against
// current authority.
func ValidatePgpArmorHeaders[A Authority](h Host[A], n plan.InstalledPgpArmorHeadersExpr) error {
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
		return fmt.Errorf("stuntgres: stale installed pgp_armor_headers declaration")
	}
	n.Procedure, n.RoutineReference = current, current
	if err := stored.ValidateInstalledPgpArmorHeaders(n, auth.Access().Catalog); err != nil {
		return err
	}
	return auth.Access().Check(auth, access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege})
}
