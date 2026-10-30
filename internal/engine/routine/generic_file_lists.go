package routine

import (
	"iter"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// genericFileRoutines lists, in OID order, the restricted file access and
// logfile inquiries whose raw NULL ACL must survive Dump/Restore instead of
// bootstrap reseeding.
var genericFileRoutines = [...]ids.OID{
	2623, 2624, 2625, 3293, 3295, 3297, 3307, 3353, 3354, 3800, 3801, 3826,
	3827, 3828, 5029, 5030, 5031, 6208, 6209, 6270, 6271, 6272, 6400,
}

// GenericFileRoutine identifies a routine of GenericFileRoutines.
func GenericFileRoutine(oid ids.OID) bool {
	_, found := slices.BinarySearch(genericFileRoutines[:], oid)
	return found
}

// GenericFileRoutines iterates the OIDs of the restricted file access and
// logfile inquiry routines.
func GenericFileRoutines() iter.Seq[ids.OID] { return slices.Values(genericFileRoutines[:]) }

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
	case 3353, 3354, 6271, 6270, 6272, 6400, 5031, 5029, 5030, 3800, 3801:
		return true
	}
	return false
}
