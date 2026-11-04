package routine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func RestrictedReplicationOriginRoutine(oid ids.OID) bool {
	switch oid {
	case 6003, 6004, 6005, 6006, 6007, 6008, 6009, 6010, 6011, 6012, 6013, 6014:
		return true
	}
	return false
}

// SeedReplicationOriginACLs changes fresh bootstrap descriptors only; later
// invocation and reconstruction use ordinary mutable procedure ACL authority.
func SeedReplicationOriginACLs(procedures []catalog.Procedure, owner authorization.PrincipalRef) {
	for i := range procedures {
		p := &procedures[i]
		if !RestrictedReplicationOriginRoutine(p.Address().ObjectID) {
			continue
		}
		p.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege}}}
	}
}
