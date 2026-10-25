package engine

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
)

// RoleHost is the family host of the role decisions: it implements role.Host
// and role.StageHost. The role decisions made outside a stage get a host with
// no stage: none of the role.Host methods reads it. ResolveRelation,
// AddDomainBase, RemoveObject and OtherDatabaseDependencies come from DDLHost.
type RoleHost struct {
	DDLHost
}

var (
	_ role.Host      = RoleHost{}
	_ role.StageHost = RoleHost{}
)

func (h RoleHost) ArgumentTypes(a ddl.Authority) (func(context.Context, parser.TypeName) (uint32, error), error) {
	types, err := CaptureRegtypeInput(DDLAuthorityOf(a), h.Op)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, raw parser.TypeName) (uint32, error) {
		return types.ResolveParsedTypeName(ctx, raw, h.Op)
	}, nil
}

func (h RoleHost) CanonicalParameter(name string) (string, error) {
	return role.CanonicalGrantParameter(h.E.Settings, name)
}

func (h RoleHost) GrantParameter(a ddl.Authority, key string) (*catalog.Parameter, error) {
	return role.GrantParameterDescriptorLocked(h.E.Settings, DDLAuthorityOf(a).Catalog, key, &h.E.RoleStore.Domain, h.E.Catalog.AllocateOIDLocked, h.E.Catalog.RevisionLocked)
}

func (h RoleHost) DescribeRoleDependency(a ddl.Authority, dep access.RoleDependency) string {
	auth := DDLAuthorityOf(a)
	path, _ := ReferencePath(auth)
	return role.DependencyDescription(auth.View, path, dep)
}

func (h RoleHost) OwnerStage() role.OwnedCleanupStage { return h.CatalogStage }
