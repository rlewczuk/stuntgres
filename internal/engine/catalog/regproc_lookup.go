package catalog

import (
	"slices"
	"strings"

	"stuntgres/internal/engine/ids"
)

// RoutineIdentity is lookup-only metadata, deliberately not a Descriptor.
// Alias lookup itself never grants execution or expands pg_proc. Explicit
// selected public projections may read these facts without installing routines.
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

func indexBuiltinRoutineIdentities() (map[ids.OID]builtinRoutineMetadata, map[string][]RoutineIdentity, map[builtinRoutineKey]RoutineIdentity) {
	byOID := make(map[ids.OID]builtinRoutineMetadata, len(builtinRoutineIdentities))
	byName := make(map[string][]RoutineIdentity)
	bySignature := make(map[builtinRoutineKey]RoutineIdentity, len(builtinRoutineIdentities))
	for _, p := range builtinRoutineIdentities {
		byOID[p.ID] = p
		byName[p.Name] = append(byName[p.Name], p.RoutineIdentity)
		bySignature[builtinRoutineKey{p.Name, p.Signature}] = p.RoutineIdentity
	}
	return byOID, byName, bySignature
}

// BuiltinRoutineIdentities returns a copy of the released lookup-only builtin
// routine identities in source order.
func BuiltinRoutineIdentities() []RoutineIdentity {
	identities := make([]RoutineIdentity, len(builtinRoutineIdentities))
	for i, p := range builtinRoutineIdentities {
		identities[i] = p.RoutineIdentity
	}
	return identities
}

// BuiltinRoutineIdentityByOID returns the released lookup-only builtin
// routine identity with this OID.
func BuiltinRoutineIdentityByOID(id ids.OID) (RoutineIdentity, bool) {
	p, ok := builtinRoutineIdentityByOID[id]
	return p.RoutineIdentity, ok
}

// BuiltinRoutineResultByOID returns genuine released result metadata without
// granting callability, requiring a local type descriptor, or adding pg_proc rows.
func BuiltinRoutineResultByOID(id ids.OID) (ids.OID, bool) {
	p, ok := builtinRoutineIdentityByOID[id]
	return p.Result, ok
}

// BuiltinRoutineIdentitiesNamed returns a copy of the released lookup-only
// builtin routine identities with this name, in source order.
func BuiltinRoutineIdentitiesNamed(name string) []RoutineIdentity {
	return slices.Clone(builtinRoutineIdentitiesByName[name])
}

// HasBuiltinInternalRoutine reports lookup-only INTERNAL signatures. It routes
// explicit calls through type selection and safety checks, never provider lookup.
func HasBuiltinInternalRoutine(name string) bool {
	for _, identity := range builtinRoutineIdentitiesByName[name] {
		if builtinRoutineIdentityByOID[identity.ID].Result == 2281 {
			return true
		}
		for part := range strings.SplitSeq(string(identity.Signature), ",") {
			if part == "2281" {
				return true
			}
		}
	}
	return false
}

// BuiltinRoutineIdentityBySignature returns the released lookup-only builtin
// routine identity with this name and signature.
func BuiltinRoutineIdentityBySignature(name string, signature ProcedureSignature) (RoutineIdentity, bool) {
	p, ok := builtinRoutineIdentityBySignature[builtinRoutineKey{name, signature}]
	return p, ok
}
