package engine

import (
	"context"

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
	types, err := h.e.captureRegtypeInput(ddlAuthorityOf(a), h.op)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, raw parser.TypeName) (uint32, error) {
		return types.ResolveParsedTypeName(ctx, raw, h.op)
	}, nil
}

func (h ddlHost) CanonicalParameter(name string) (string, error) {
	return h.e.canonicalGrantParameter(name)
}

func (h ddlHost) GrantParameter(a ddl.Authority, key string) (*catalog.Parameter, error) {
	return h.e.grantParameterDescriptorLocked(ddlAuthorityOf(a), key)
}

func (h ddlHost) ReferencingFeatures(ref authorization.PrincipalRef) []string {
	return h.e.roles.referencingFeatures(ref)
}

func (h ddlHost) DescribeRoleDependency(a ddl.Authority, dep access.RoleDependency) string {
	auth := ddlAuthorityOf(a)
	path, _ := referencePath(auth)
	return role.DependencyDescription(auth.View, path, dep)
}

func (h ddlHost) OwnerStage() role.OwnedCleanupStage { return h.stage }
