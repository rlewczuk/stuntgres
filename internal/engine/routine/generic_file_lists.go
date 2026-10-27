package routine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// GenericFileListRoutine identifies restricted builtins whose raw NULL ACL must
// survive Dump/Restore rather than being replaced with their bootstrap ACL.
func GenericFileListRoutine(oid ids.OID) bool {
	switch oid {
	case 2625, 3297, 3353, 3354, 6271, 6270, 6272, 6400, 5031:
		return true
	}
	return false
}

// SeedGenericFileListACLs modifies only fresh, engine-owned builtin descriptors.
// Grants remain ordinary mutable procedure ACLs after installation.
func SeedGenericFileListACLs(procedures []catalog.Procedure, owner, monitor authorization.PrincipalRef) {
	for i := range procedures {
		p := &procedures[i]
		if !GenericFileListRoutine(p.Address().ObjectID) {
			continue
		}
		entries := []authorization.ACLEntry{{
			Grantor:    owner,
			Grantee:    authorization.ACLGrantee{Role: owner},
			Privileges: authorization.ExecutePrivilege,
		}}
		if p.Address().ObjectID != 2625 && p.Address().ObjectID != 3297 {
			entries = append(entries, authorization.ACLEntry{
				Grantor:    owner,
				Grantee:    authorization.ACLGrantee{Role: monitor},
				Privileges: authorization.ExecutePrivilege,
			})
		}
		p.ACL = authorization.ACLValue{Explicit: true, Entries: entries}
	}
}
