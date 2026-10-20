package catalog

import (
	"slices"

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

var builtinRoutineIdentityByOID = indexBuiltinRoutineIdentities()

func indexBuiltinRoutineIdentities() map[ids.OID]RoutineIdentity {
	byOID := make(map[ids.OID]RoutineIdentity, len(builtinRoutineIdentities))
	for _, p := range builtinRoutineIdentities {
		byOID[p.ID] = p
	}
	return byOID
}

// BuiltinRoutineIdentities returns a copy of the released lookup-only builtin
// routine identities in source order.
func BuiltinRoutineIdentities() []RoutineIdentity {
	return slices.Clone(builtinRoutineIdentities[:])
}

// BuiltinRoutineIdentityByOID returns the released lookup-only builtin
// routine identity with this OID.
func BuiltinRoutineIdentityByOID(id ids.OID) (RoutineIdentity, bool) {
	p, ok := builtinRoutineIdentityByOID[id]
	return p, ok
}
