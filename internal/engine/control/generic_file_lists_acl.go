package control

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

// Omit only the ordered raw authority recreated by the destination seed.
func genericFileACLReconstructed(p *catalog.Procedure, bootstrap, monitor authorization.PrincipalRef) bool {
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
	case 3826:
		name, signature, entries = "pg_read_file", "25", 1
	case 6208:
		name, signature, entries = "pg_read_file", "25,16", 1
	case 2624:
		name, signature, entries = "pg_read_file", "25,20,20", 1
	case 3293:
		name, signature, entries = "pg_read_file", "25,20,20,16", 1
	case 3828:
		name, signature, entries = "pg_read_binary_file", "25", 1
	case 6209:
		name, signature, entries = "pg_read_binary_file", "25,16", 1
	case 3827:
		name, signature, entries = "pg_read_binary_file", "25,20,20", 1
	case 3295:
		name, signature, entries = "pg_read_binary_file", "25,20,20,16", 1
	case 2623:
		name, signature, entries = "pg_stat_file", "25", 1
	case 3307:
		name, signature, entries = "pg_stat_file", "25,16", 1
	case 5029:
		name = "pg_ls_tmpdir"
	case 5030:
		name, signature = "pg_ls_tmpdir", "26"
	case 3800:
		name = "pg_current_logfile"
	case 3801:
		name, signature = "pg_current_logfile", "25"
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
