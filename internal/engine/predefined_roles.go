package engine

import "fmt"

const (
	bootstrapPrincipalID uint64 = 10
	databaseOwnerRoleID  uint64 = 6171
	monitorRoleID        uint64 = 3373
	readAllDataRoleID    uint64 = 6181
	writeAllDataRoleID   uint64 = 6182
	maintainRoleID       uint64 = 6337
	lastPredefinedRoleID uint64 = 6392
)

// Only initialization consumes this table. All subsequent identity, membership,
// pinned-dependency and capability decisions use the immutable role authority.
var predefinedRoleDefinitions = [...]struct {
	name string
	id   uint64
}{
	{"pg_checkpoint", 4544}, {"pg_create_subscription", 6304},
	{"pg_database_owner", databaseOwnerRoleID}, {"pg_maintain", maintainRoleID},
	{"pg_monitor", monitorRoleID}, {"pg_read_all_settings", 3374},
	{"pg_read_all_stats", 3375}, {"pg_stat_scan_tables", 3377},
	{"pg_read_all_data", readAllDataRoleID}, {"pg_write_all_data", writeAllDataRoleID},
	{"pg_read_server_files", 4569}, {"pg_write_server_files", 4570},
	{"pg_execute_server_program", 4571}, {"pg_signal_autovacuum_worker", 6392},
	{"pg_signal_backend", 4200}, {"pg_use_reserved_connections", 4550},
}

func (e *Engine) initializeRoles() {
	bootstrap := PrincipalRef{e, bootstrapPrincipalID}
	r := &roleCatalog{revision: 1, roles: make(map[PrincipalRef]RoleDescriptor, 17), names: make(map[string]PrincipalRef, 17)}
	r.roles[bootstrap] = RoleDescriptor{Ref: bootstrap, Name: "postgres", Login: true, Superuser: true, CreateDB: true, CreateRole: true, Inherit: true, Replication: true, BypassRLS: true, ConnectionLimit: -1}
	r.names["postgres"] = bootstrap
	for _, definition := range predefinedRoleDefinitions {
		ref := PrincipalRef{e, definition.id}
		r.roles[ref] = RoleDescriptor{Ref: ref, Name: definition.name, Inherit: true, ConnectionLimit: -1, predefined: true}
		r.names[definition.name] = ref
	}
	r.edges = make([]Membership, 3)
	for i, id := range [...]uint64{3374, 3375, 3377} {
		r.edges[i] = Membership{Role: PrincipalRef{e, id}, Member: PrincipalRef{e, monitorRoleID}, Grantor: bootstrap, Inherit: true, Set: true}
	}
	e.roles = r
	e.nextPrincipal = lastPredefinedRoleID + 1
}

// This rule is database-local and never materialized as a grantable edge. The
// bounded single-engine database owner is the bootstrap principal, not a DSN or
// whichever actor happens to be current_user.
func (r RoleRead) implicitDatabaseOwner(member PrincipalRef) PrincipalRef {
	if member == (PrincipalRef{r.owner, bootstrapPrincipalID}) {
		return PrincipalRef{r.owner, databaseOwnerRoleID}
	}
	return PrincipalRef{}
}

// Return a private candidate; only successful publication advances allocation.
func (e *Engine) nextRolePrincipalLocked() (PrincipalRef, error) {
	id := e.nextPrincipal
	if id == 0 || id == ^uint64(0) {
		return PrincipalRef{}, fmt.Errorf("stuntgres: principal identity or authorization revision exhausted")
	}
	if id <= lastPredefinedRoleID {
		id = lastPredefinedRoleID + 1
	}
	ref := PrincipalRef{e, id}
	if _, exists := e.roles.roles[ref]; exists {
		return PrincipalRef{}, fmt.Errorf("stuntgres: principal identity already allocated")
	}
	return ref, nil
}

func predefinedPrivileges(d Descriptor, principals []PrincipalRef) Privileges {
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
	if d.Kind() == NamespaceObject {
		if rights&(SelectPrivilege|InsertPrivilege|UpdatePrivilege|DeletePrivilege) != 0 {
			return UsagePrivilege
		}
		return 0
	}
	r, ok := d.(*relationDescriptor)
	if !ok {
		return 0
	}
	if r.virtualView || r.relationKind == catalogRelation {
		return rights & SelectPrivilege
	}
	switch r.relationKind {
	case sequenceRelation:
		return rights & (SelectPrivilege | UpdatePrivilege)
	case tableRelation, partitionedRelation, viewRelation, materializedViewRelation:
		return rights
	}
	return 0
}
