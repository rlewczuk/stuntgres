package routine

import (
	"iter"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

var restrictedWALPositionRoutines = [...]ids.OID{2848, 3098, 6305}

// RestrictedWALPositionRoutine identifies the released owner-only seeds.
func RestrictedWALPositionRoutine(oid ids.OID) bool {
	_, found := slices.BinarySearch(restrictedWALPositionRoutines[:], oid)
	return found
}

// RestrictedWALPositionRoutines enumerates the restricted WAL identities.
func RestrictedWALPositionRoutines() iter.Seq[ids.OID] {
	return slices.Values(restrictedWALPositionRoutines[:])
}

// SeedWALPositionACLs modifies fresh engine-owned descriptors only. EXECUTE
// thereafter follows ordinary mutable ACLs, including raw NULL default semantics.
func SeedWALPositionACLs(procedures []catalog.Procedure, owner authorization.PrincipalRef) {
	for i := range procedures {
		p := &procedures[i]
		if !RestrictedWALPositionRoutine(p.Address().ObjectID) { continue }
		p.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{
			Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege,
		}}}
	}
}
