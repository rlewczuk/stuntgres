package catalog

import (
	"stuntgres/internal/engine/ids"
)

// RoutineIdentity is lookup-only metadata. It is deliberately not a Descriptor:
// finding a routine alias must never grant execution or expand pg_proc SQL rows.
type RoutineIdentity struct {
	ID        ids.OID
	Namespace ids.OID
	Name      string
	Signature ProcedureSignature
}
