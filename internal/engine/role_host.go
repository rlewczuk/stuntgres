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
	_ role.Host      = ddlHost{}
	_ role.StageHost = ddlHost{}
)

func (h ddlHost) ResolveRelation(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.e.resolveRelationReference(ddlAuthorityOf(a), name, true)
}

func (h ddlHost) ArgumentTypes(a ddl.Authority) (func(context.Context, parser.TypeName) (uint32, error), error) {
	types, err := captureRegtypeInput(ddlAuthorityOf(a), h.op)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, raw parser.TypeName) (uint32, error) {
		return types.ResolveParsedTypeName(ctx, raw, h.op)
	}, nil
}

func (h ddlHost) CanonicalParameter(name string) (string, error) {
	return role.CanonicalGrantParameter(h.e.settings, name)
}

func (h ddlHost) GrantParameter(a ddl.Authority, key string) (*catalog.Parameter, error) {
	return role.GrantParameterDescriptorLocked(h.e.settings, ddlAuthorityOf(a).Catalog, key, &h.e.roles.Domain, h.e.catalog.AllocateOIDLocked, h.e.catalog.RevisionLocked)
}

func (h ddlHost) OtherDatabaseDependencies(ref authorization.PrincipalRef) []access.RoleDependency {
	var result []access.RoleDependency
	for _, target := range h.e.cluster.targets {
		if target == h.e {
			continue
		}
		var dependencies []access.RoleDependency
		for address, descriptor := range target.catalog.State.Descriptors() {
			if address.ClassID != ids.DatabaseClassID {
				access.DescriptorRoleDependencies(descriptor, ref, &dependencies)
			}
		}
		view := target.catalog.Current()
		access.DefaultRoleDependencies(view, target.roles.PublishedRead(&target.catalog.Owner, view), ref, &dependencies)
		if len(dependencies) != 0 {
			kind := "objects"
			if len(dependencies) == 1 {
				kind = "object"
			}
			result = append(result, access.RoleDependency{Kind: 'a', Description: fmt.Sprintf("%d %s in database %s", len(dependencies), kind, target.databaseName)})
		}
	}
	return result
}

func (h ddlHost) DescribeRoleDependency(a ddl.Authority, dep access.RoleDependency) string {
	auth := ddlAuthorityOf(a)
	path, _ := referencePath(auth)
	return role.DependencyDescription(auth.View, path, dep)
}

func (h ddlHost) OwnerStage() role.OwnedCleanupStage { return h.stage }
