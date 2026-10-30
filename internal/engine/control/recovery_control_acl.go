package control

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

// Omit only the exact raw authority recreated by recovery-control bootstrap.
// Raw NULL, explicit empty, grants and modified identities require normal repair.
func recoveryControlACLReconstructed(p *catalog.Procedure, bootstrap authorization.PrincipalRef) bool {
	if p.Address().ClassID != ids.ProcedureClassID || p.Address().SubID != 0 || !routine.RestrictedRecoveryControlRoutine(p.Address().ObjectID) || p.Builtin == nil || p.Builtin != routine.BuiltinRoutine(p.Address().ObjectID) || p.Owner != bootstrap || p.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) {
		return false
	}
	identity := p.Builtin.Identity
	if p.Name() != identity.Name || p.Signature != identity.Signature || !p.ACL.Explicit || len(p.ACL.Entries) != 1 {
		return false
	}
	return p.ACL.Entries[0] == (authorization.ACLEntry{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Role: bootstrap}, Privileges: authorization.ExecutePrivilege})
}
