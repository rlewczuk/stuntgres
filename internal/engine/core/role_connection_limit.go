package core

import (
	"stuntgres/internal/engine/authorization"
)

// The caller holds cluster metadata. Canonical admission accounting includes
// reserved, initializing, live and ending physical connections across targets.
// Session publication and SET ROLE never change the retained login principal.
func (e *Engine) roleSessionCountLocked(ref authorization.PrincipalRef) int {
	e.Cluster.AdmissionMu.Lock()
	defer e.Cluster.AdmissionMu.Unlock()
	return e.Cluster.principalAdmissions[ref]
}
