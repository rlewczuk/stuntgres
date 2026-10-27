package routine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// GenericFileRoutine identifies restricted builtins whose raw NULL ACL must
// survive Dump/Restore rather than being replaced with their bootstrap ACL.
func GenericFileRoutine(oid ids.OID) bool {
	switch oid {
	case 2625, 3297, 3353, 3354, 6271, 6270, 6272, 6400, 5031,
		3826, 6208, 2624, 3293, 3828, 6209, 3827, 3295, 2623, 3307, 5029, 5030:
		return true
	}
	return false
}

// SeedGenericFileACLs modifies only fresh, engine-owned builtin descriptors.
// Grants remain ordinary mutable procedure ACLs after installation.
func SeedGenericFileACLs(procedures []catalog.Procedure, owner, monitor authorization.PrincipalRef) {
	for i := range procedures {
		p := &procedures[i]
		if !GenericFileRoutine(p.Address().ObjectID) {
			continue
		}
		entries := []authorization.ACLEntry{{
			Grantor:    owner,
			Grantee:    authorization.ACLGrantee{Role: owner},
			Privileges: authorization.ExecutePrivilege,
		}}
		if GenericFileMonitorRoutine(p.Address().ObjectID) {
			entries = append(entries, authorization.ACLEntry{
				Grantor:    owner,
				Grantee:    authorization.ACLGrantee{Role: monitor},
				Privileges: authorization.ExecutePrivilege,
			})
		}
		p.ACL = authorization.ACLValue{Explicit: true, Entries: entries}
	}
}

// GenericFileMonitorRoutine identifies the released pg_monitor EXECUTE seeds.
func GenericFileMonitorRoutine(oid ids.OID) bool {
	switch oid {
	case 3353, 3354, 6271, 6270, 6272, 6400, 5031, 5029, 5030:
		return true
	}
	return false
}
