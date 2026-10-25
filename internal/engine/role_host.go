package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
)

// ddlHost implements role.Host and role.StageHost. The role decisions made
// outside a stage get a ddlHost{e, op} with no stage: none of the role.Host
// methods reads it. AddDomainBase (typedef_host.go) and RemoveObject
// (schema_host.go) are shared with the other feature hosts.
var (
	_ role.Host      = DDLHost{}
	_ role.StageHost = DDLHost{}
)

func (h DDLHost) ResolveRelation(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.E.ResolveRelationReference(DDLAuthorityOf(a), name, true)
}

func (h DDLHost) ArgumentTypes(a ddl.Authority) (func(context.Context, parser.TypeName) (uint32, error), error) {
	types, err := CaptureRegtypeInput(DDLAuthorityOf(a), h.Op)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, raw parser.TypeName) (uint32, error) {
		return types.ResolveParsedTypeName(ctx, raw, h.Op)
	}, nil
}

func (h DDLHost) CanonicalParameter(name string) (string, error) {
	return role.CanonicalGrantParameter(h.E.Settings, name)
}

func (h DDLHost) GrantParameter(a ddl.Authority, key string) (*catalog.Parameter, error) {
	return role.GrantParameterDescriptorLocked(h.E.Settings, DDLAuthorityOf(a).Catalog, key, &h.E.RoleStore.Domain, h.E.Catalog.AllocateOIDLocked, h.E.Catalog.RevisionLocked)
}

func (h DDLHost) OtherDatabaseDependencies(ref authorization.PrincipalRef) []access.RoleDependency {
	var result []access.RoleDependency
	for _, target := range h.E.Cluster.Targets {
		if target == h.E {
			continue
		}
		var dependencies []access.RoleDependency
		for address, descriptor := range target.Catalog.State.Descriptors() {
			if address.ClassID != ids.DatabaseClassID {
				access.DescriptorRoleDependencies(descriptor, ref, &dependencies)
			}
		}
		view := target.Catalog.Current()
		access.DefaultRoleDependencies(view, target.RoleStore.PublishedRead(&target.Catalog.Owner, view), ref, &dependencies)
		if len(dependencies) != 0 {
			kind := "objects"
			if len(dependencies) == 1 {
				kind = "object"
			}
			result = append(result, access.RoleDependency{Kind: 'a', Description: fmt.Sprintf("%d %s in database %s", len(dependencies), kind, target.DatabaseName)})
		}
	}
	return result
}

func (h DDLHost) DescribeRoleDependency(a ddl.Authority, dep access.RoleDependency) string {
	auth := DDLAuthorityOf(a)
	path, _ := ReferencePath(auth)
	return role.DependencyDescription(auth.View, path, dep)
}

func (h DDLHost) OwnerStage() role.OwnedCleanupStage { return h.CatalogStage }
