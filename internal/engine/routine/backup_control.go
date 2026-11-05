package routine

import (
	"iter"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

var restrictedBackupControlRoutines = [...]ids.OID{2172, 2739}

// RestrictedBackupControlRoutine identifies the released owner-only seeds.
func RestrictedBackupControlRoutine(oid ids.OID) bool {
	_, found := slices.BinarySearch(restrictedBackupControlRoutines[:], oid)
	return found
}

// RestrictedBackupControlRoutines enumerates the restricted backup identities.
func RestrictedBackupControlRoutines() iter.Seq[ids.OID] {
	return slices.Values(restrictedBackupControlRoutines[:])
}

// SeedBackupControlACLs modifies fresh engine-owned descriptors only. EXECUTE
// thereafter follows ordinary mutable ACLs, including raw NULL default semantics.
func SeedBackupControlACLs(procedures []catalog.Procedure, owner authorization.PrincipalRef) {
	for i := range procedures {
		p := &procedures[i]
		if !RestrictedBackupControlRoutine(p.Address().ObjectID) {
			continue
		}
		p.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{
			Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege,
		}}}
	}
}
