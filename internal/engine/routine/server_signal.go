package routine

import (
	"iter"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

var restrictedServerSignalRoutines = [...]ids.OID{2621, 2622, 4543}

// RestrictedServerSignalRoutine identifies the released owner-only seeds.
func RestrictedServerSignalRoutine(oid ids.OID) bool {
	_, found := slices.BinarySearch(restrictedServerSignalRoutines[:], oid)
	return found
}

// RestrictedServerSignalRoutines enumerates the restricted server signal identities.
func RestrictedServerSignalRoutines() iter.Seq[ids.OID] {
	return slices.Values(restrictedServerSignalRoutines[:])
}

// SeedServerSignalACLs modifies fresh engine-owned descriptors only. EXECUTE
// thereafter follows ordinary mutable ACLs, including raw NULL default semantics.
func SeedServerSignalACLs(procedures []catalog.Procedure, owner authorization.PrincipalRef) {
	for i := range procedures {
		p := &procedures[i]
		if !RestrictedServerSignalRoutine(p.Address().ObjectID) { continue }
		p.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{
			Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege,
		}}}
	}
}
