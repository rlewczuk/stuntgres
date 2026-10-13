package authorization

const (
	BootstrapPrincipalID uint64 = 10
	DatabaseOwnerRoleID  uint64 = 6171
	monitorRoleID        uint64 = 3373
	readAllDataRoleID    uint64 = 6181
	writeAllDataRoleID   uint64 = 6182
	maintainRoleID       uint64 = 6337
	LastPredefinedRoleID uint64 = 6392
)

// Only initialization consumes this table. All subsequent identity, membership,
// pinned-dependency and capability decisions use the immutable role authority.
var predefinedRoleDefinitions = [...]struct {
	name string
	id   uint64
}{
	{"pg_checkpoint", 4544}, {"pg_create_subscription", 6304},
	{"pg_database_owner", DatabaseOwnerRoleID}, {"pg_maintain", maintainRoleID},
	{"pg_monitor", monitorRoleID}, {"pg_read_all_settings", 3374},
	{"pg_read_all_stats", 3375}, {"pg_stat_scan_tables", 3377},
	{"pg_read_all_data", readAllDataRoleID}, {"pg_write_all_data", writeAllDataRoleID},
	{"pg_read_server_files", 4569}, {"pg_write_server_files", 4570},
	{"pg_execute_server_program", 4571}, {"pg_signal_autovacuum_worker", 6392},
	{"pg_signal_backend", 4200}, {"pg_use_reserved_connections", 4550},
}

func Seed(domain *Domain) Root {
	bootstrap := PrincipalRef{domain, BootstrapPrincipalID}
	r := Root{domain: domain, roles: make(map[PrincipalRef]RoleDescriptor, 17), names: make(map[string]PrincipalRef, 17)}
	r.roles[bootstrap] = RoleDescriptor{Ref: bootstrap, Name: "postgres", Login: true, Superuser: true, CreateDB: true, CreateRole: true, Inherit: true, Replication: true, BypassRLS: true, ConnectionLimit: -1}
	r.names["postgres"] = bootstrap
	for _, definition := range predefinedRoleDefinitions {
		ref := PrincipalRef{domain, definition.id}
		r.roles[ref] = RoleDescriptor{Ref: ref, Name: definition.name, Inherit: true, ConnectionLimit: -1, predefined: true}
		r.names[definition.name] = ref
	}
	r.edges = make([]Membership, 3)
	for i, id := range [...]uint64{3374, 3375, 3377} {
		r.edges[i] = Membership{Role: PrincipalRef{domain, id}, Member: PrincipalRef{domain, monitorRoleID}, Grantor: bootstrap, Inherit: true, Set: true}
	}
	return r
}

func (r Read) implicitDatabaseOwner(member PrincipalRef) PrincipalRef {
	if member == r.databaseOwner && member != (PrincipalRef{}) {
		return PrincipalRef{r.owner, DatabaseOwnerRoleID}
	}
	return PrincipalRef{}
}
func PredefinedPrivileges(principals []PrincipalRef) Privileges {
	var rights Privileges
	for _, principal := range principals {
		switch principal.id {
		case readAllDataRoleID:
			rights |= SelectPrivilege
		case writeAllDataRoleID:
			rights |= InsertPrivilege | UpdatePrivilege | DeletePrivilege
		case maintainRoleID:
			rights |= MaintainPrivilege
		}
	}
	return rights
}
