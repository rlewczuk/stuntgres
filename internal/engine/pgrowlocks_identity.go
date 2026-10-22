package engine

import (
	"fmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func (h *statementHost) ValidatePgrowlocks(n plan.InstalledPgrowlocksExpr) error {
	s, e := h.scope(), h.scope().engine()
	if err := s.active(); err != nil {
		return err
	}
	e.mu.Lock()
	auth, err := s.writeAuthorizationLocked(e)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	current, ok := auth.Catalog.Procedure(n.Ref.Address)
	if !ok || n.RoutineReference != n.Procedure || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return fmt.Errorf("stuntgres: stale installed pgrowlocks declaration")
	}
	n.Procedure, n.RoutineReference = current, current
	return stored.ValidateInstalledPgrowlocks(n, auth.Catalog)
}
