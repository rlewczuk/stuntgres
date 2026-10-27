package control

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

// Omit only the ordered raw authority recreated by the destination seed.
func genericFileListACLReconstructed(p *catalog.Procedure, bootstrap, monitor authorization.PrincipalRef) bool {
	if p.Address().ClassID != ids.ProcedureClassID || p.Builtin == nil || p.Builtin != routine.BuiltinRoutine(p.Address().ObjectID) || p.Owner != bootstrap || p.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) {
		return false
	}
	var name, signature string
	entries := 2
	switch p.Address().ObjectID {
	case 2625:
		name, signature, entries = "pg_ls_dir", "25", 1
	case 3297:
		name, signature, entries = "pg_ls_dir", "25,16,16", 1
	case 3353:
		name = "pg_ls_logdir"
	case 3354:
		name = "pg_ls_waldir"
	case 6270:
		name = "pg_ls_logicalsnapdir"
	case 6271:
		name = "pg_ls_logicalmapdir"
	case 6272:
		name, signature = "pg_ls_replslotdir", "25"
	case 6400:
		name = "pg_ls_summariesdir"
	case 5031:
		name = "pg_ls_archive_statusdir"
	default:
		return false
	}
	if p.Name() != name || string(p.Signature) != signature || !p.ACL.Explicit || len(p.ACL.Entries) != entries {
		return false
	}
	if p.ACL.Entries[0] != (authorization.ACLEntry{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Role: bootstrap}, Privileges: authorization.ExecutePrivilege}) {
		return false
	}
	return entries == 1 || p.ACL.Entries[1] == (authorization.ACLEntry{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Role: monitor}, Privileges: authorization.ExecutePrivilege})
}
