package routine

import (
 "iter"
 "slices"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
)
var restrictedStatsResetRoutines = [...]ids.OID{6170,6232}
func RestrictedStatsResetRoutine(oid ids.OID) bool { _,found:=slices.BinarySearch(restrictedStatsResetRoutines[:],oid); return found }
func RestrictedStatsResetRoutines() iter.Seq[ids.OID] { return slices.Values(restrictedStatsResetRoutines[:]) }
// SeedStatsResetACLs operates only on fresh engine-owned declarations. Ordinary
// mutable EXECUTE admission remains authoritative after bootstrap.
func SeedStatsResetACLs(procedures []catalog.Procedure, owner authorization.PrincipalRef) {
 for i:=range procedures {
  p:=&procedures[i]; if !RestrictedStatsResetRoutine(p.Address().ObjectID) { continue }
  p.ACL=authorization.ACLValue{Explicit:true,Entries:[]authorization.ACLEntry{{Grantor:owner,Grantee:authorization.ACLGrantee{Role:owner},Privileges:authorization.ExecutePrivilege}}}
 }
}
