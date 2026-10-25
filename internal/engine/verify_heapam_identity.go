package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func (h *statementHost) ValidateVerifyHeapam(n plan.InstalledVerifyHeapamExpr) error {
	s, e := h.scope(), h.scope().Engine()
	if err := s.Active(); err != nil { return err }
	e.Mu.Lock()
	auth, err := s.writeAuthorizationLocked(e)
	e.Mu.Unlock()
	if err != nil { return err }
	current, ok := auth.Catalog.Procedure(n.Ref.Address)
	if !ok || n.RoutineReference != n.Procedure || !exec.SameRoutineDeclaration(current, n.Procedure) { return fmt.Errorf("stuntgres: stale installed verify_heapam declaration") }
	n.Procedure, n.RoutineReference = current, current
	if err := stored.ValidateInstalledVerifyHeapam(n, auth.Catalog); err != nil { return err }
	return auth.View.Check(auth, access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege})
}
