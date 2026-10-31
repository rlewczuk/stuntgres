package core

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

// Roots are retained under the shared mutex, traversed only after unlocking.
type sharedDependencyDatabase struct {
	Database ids.OID
	Catalog  catalog.Read
	Current  bool
}

// captureSharedDependencyRootsLocked shares the inquiry's acquisition frontier.
// The caller holds the common cluster mutex; no source may recapture peers later.
func (e *Engine) captureSharedDependencyRootsLocked(ctx context.Context, inquiry *inquirySnapshot) error {
	roots := inquiry.sharedDependencySingle[:]
	if len(e.Cluster.Targets) > 1 {
		roots = make([]sharedDependencyDatabase, len(e.Cluster.Targets))
	}
	i := 0
	for _, target := range e.Cluster.Targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		view := target.Catalog.Current()
		if target == e {
			view = inquiry.CommentCatalog
		}
		roots[i] = sharedDependencyDatabase{Database: target.Catalog.DatabaseAddress().ObjectID, Catalog: view, Current: target == e}
		i++
	}
	if len(e.Cluster.Targets) > 1 {
		inquiry.sharedDependencyDatabases = roots
	}
	return nil
}

func classifySharedDependencies(ctx context.Context, view catalog.Read, roles authorization.Read, databases []sharedDependencyDatabase) ([]pgcatalog.SharedDependency, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type key struct {
		database ids.OID
		address  ids.ObjectAddress
		role     authorization.PrincipalRef
		kind     byte
	}
	seen := make(map[key]struct{})
	rows := make([]pgcatalog.SharedDependency, 0)
	var failure error
	appendEdge := func(database ids.OID, dependency access.RoleDependency, role authorization.PrincipalRef) bool {
		if failure = ctx.Err(); failure != nil {
			return false
		}
		descriptor, err := roles.Descriptor(role)
		if err != nil {
			failure = err
			return false
		}
		if role.ID() == authorization.BootstrapPrincipalID || descriptor.Predefined() {
			return true
		}
		identity := key{database, dependency.Address, role, dependency.Kind}
		if dependency.Kind == 'a' || dependency.Kind == 'i' {
			if _, ok := seen[identity]; ok {
				return true
			}
			seen[identity] = struct{}{}
		}
		rows = append(rows, pgcatalog.SharedDependency{Database: database, Address: dependency.Address, ReferenceClass: ids.RoleClassID, Reference: ids.OID(role.ID()), Kind: dependency.Kind})
		return true
	}
	current := 0
	for _, database := range databases {
		if database.Catalog.State() == nil || database.Database == 0 || database.Catalog.Owner() == nil || !roles.BelongsTo(database.Catalog.Owner().Principals) {
			return nil, fmt.Errorf("stuntgres: invalid shared dependency database authority")
		}
		if database.Current {
			current++
			if database.Catalog != view {
				return nil, fmt.Errorf("stuntgres: foreign current shared dependency authority")
			}
		}
		shared := func(address ids.ObjectAddress) bool {
			relation, ok := database.Catalog.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: address.ClassID}).(*catalog.Relation)
			return ok && catalog.SharedRelation(relation)
		}
		visit := func(role authorization.PrincipalRef, dependency access.RoleDependency) bool {
			dbid := database.Database
			if shared(dependency.Address) {
				if !database.Current {
					return true
				}
				dbid = 0
			}
			return appendEdge(dbid, dependency, role)
		}
		for _, descriptor := range database.Catalog.State().Descriptors() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if access.VisitDescriptorRoleDependencies(descriptor, visit) {
				return nil, failure
			}
		}
		for key, record := range database.Catalog.State().DefaultACLs() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if access.VisitDefaultRoleDependencies(key, record, visit) {
				return nil, failure
			}
		}
		for address, record := range database.Catalog.State().InitialPrivileges() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for role := range access.InitialACLRoleReferences(record) {
				if !visit(role, access.RoleDependency{Address: address, Kind: 'i'}) {
					return nil, failure
				}
			}
		}
	}
	if current != 1 {
		return nil, fmt.Errorf("stuntgres: missing or duplicate current shared dependency authority")
	}
	for edge := range roles.Edges() {
		if edge.OID == 0 || !roles.GroupMember(edge.Role) || !roles.GroupMember(edge.Member) || !roles.GroupMember(edge.Grantor) {
			return nil, fmt.Errorf("stuntgres: invalid stored membership identity")
		}
		if !appendEdge(0, access.RoleDependency{Address: ids.ObjectAddress{ClassID: pgcatalog.AuthMembersOID, ObjectID: ids.OID(edge.OID)}, Kind: 'a'}, edge.Grantor) {
			return nil, failure
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
