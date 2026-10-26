package core

import (
	"fmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func (h *statementHost) ValidateTablefuncNormal(n plan.InstalledTablefuncNormalExpr) error {
	s, e := h.scope(), h.scope().Engine()
	if err := s.Active(); err != nil { return err }
	e.Mu.Lock()
	auth, err := s.writeAuthorizationLocked(e)
	e.Mu.Unlock()
	if err != nil { return err }
	current, ok := auth.Catalog.Procedure(n.Ref.Address)
	if !ok || n.RoutineReference != n.Procedure || !exec.SameRoutineDeclaration(current, n.Procedure) { return fmt.Errorf("stuntgres: stale installed normal_rand declaration") }
	n.Procedure, n.RoutineReference = current, current
	return stored.ValidateInstalledTablefuncNormal(n, auth.Catalog)
}
