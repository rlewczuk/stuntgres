package routine

import (
	"iter"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

var restrictedRecoveryControlRoutines = [...]ids.OID{3071, 3072, 3436}

// RestrictedRecoveryControlRoutine identifies the released owner-only seeds.
func RestrictedRecoveryControlRoutine(oid ids.OID) bool {
	_, found := slices.BinarySearch(restrictedRecoveryControlRoutines[:], oid)
	return found
}

// RestrictedRecoveryControlRoutines enumerates the restricted recovery identities.
func RestrictedRecoveryControlRoutines() iter.Seq[ids.OID] {
	return slices.Values(restrictedRecoveryControlRoutines[:])
}

// SeedRecoveryControlACLs modifies fresh engine-owned descriptors only. EXECUTE
// thereafter follows ordinary mutable ACLs, including raw NULL default semantics.
func SeedRecoveryControlACLs(procedures []catalog.Procedure, owner authorization.PrincipalRef) {
	for i := range procedures {
		p := &procedures[i]
		if !RestrictedRecoveryControlRoutine(p.Address().ObjectID) { continue }
		p.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{
			Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege,
		}}}
	}
}
