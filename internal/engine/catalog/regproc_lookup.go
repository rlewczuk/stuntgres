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

type builtinRoutineKey struct {
	name      string
	signature ProcedureSignature
}

var builtinRoutineIdentityByOID, builtinRoutineIdentitiesByName, builtinRoutineIdentityBySignature = indexBuiltinRoutineIdentities()

func indexBuiltinRoutineIdentities() (map[ids.OID]RoutineIdentity, map[string][]RoutineIdentity, map[builtinRoutineKey]RoutineIdentity) {
	byOID := make(map[ids.OID]RoutineIdentity, len(builtinRoutineIdentities))
	byName := make(map[string][]RoutineIdentity)
	bySignature := make(map[builtinRoutineKey]RoutineIdentity, len(builtinRoutineIdentities))
	for _, p := range builtinRoutineIdentities {
		byOID[p.ID] = p
		byName[p.Name] = append(byName[p.Name], p)
		bySignature[builtinRoutineKey{p.Name, p.Signature}] = p
	}
	return byOID, byName, bySignature
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

// BuiltinRoutineIdentitiesNamed returns a copy of the released lookup-only
// builtin routine identities with this name, in source order.
func BuiltinRoutineIdentitiesNamed(name string) []RoutineIdentity {
	return slices.Clone(builtinRoutineIdentitiesByName[name])
}

// BuiltinRoutineIdentityBySignature returns the released lookup-only builtin
// routine identity with this name and signature.
func BuiltinRoutineIdentityBySignature(name string, signature ProcedureSignature) (RoutineIdentity, bool) {
	p, ok := builtinRoutineIdentityBySignature[builtinRoutineKey{name, signature}]
	return p, ok
}
