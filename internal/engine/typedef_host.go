package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// typedefHost is schemaHost seen as typedef.StageHost. The ddl.Host methods
// delegate to schemaHost; only the type methods have bodies here. 20b merges
// the two adapters.
type typedefHost schemaHost

var _ typedef.StageHost = typedefHost{}

func (h typedefHost) AccessView(a ddl.Authority) access.View { return schemaHost(h).AccessView(a) }
func (h typedefHost) Recapture(a ddl.Authority, view catalog.Read) ddl.Authority {
	return schemaHost(h).Recapture(a, view)
}
func (h typedefHost) CheckCreateIn(a ddl.Authority, namespace ids.ObjectRef) error {
	return schemaHost(h).CheckCreateIn(a, namespace)
}
func (h typedefHost) Context() context.Context { return schemaHost(h).Context() }
func (h typedefHost) EmitNotice(level session.NoticeLevel, diagnostic sqlstate.Error) {
	schemaHost(h).EmitNotice(level, diagnostic)
}
func (h typedefHost) AllocateOID() (ids.OID, error)             { return schemaHost(h).AllocateOID() }
func (h typedefHost) Revision() (ids.DefinitionRevision, error) { return schemaHost(h).Revision() }
func (h typedefHost) Stage() ddl.Stage                          { return schemaHost(h).Stage() }
func (h typedefHost) StageBase() catalog.Read                   { return schemaHost(h).StageBase() }
func (h typedefHost) StageRead() catalog.Read                   { return schemaHost(h).StageRead() }
func (h typedefHost) CreationACL(owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	return schemaHost(h).CreationACL(owner, schema, class)
}
func (h typedefHost) DropTarget(address ids.ObjectAddress) { schemaHost(h).DropTarget(address) }
func (h typedefHost) RecordDependencyPath(a ddl.Authority) { schemaHost(h).RecordDependencyPath(a) }
func (h typedefHost) CreationNamespace(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, ddl.Authority, error) {
	return schemaHost(h).CreationNamespace(a, name)
}
func (h typedefHost) ExistingCreationNamespace(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return schemaHost(h).ExistingCreationNamespace(a, name)
}
func (h typedefHost) SearchNamespaces(a ddl.Authority, name parser.ObjectName, class ids.OID) ([]ids.ObjectAddress, error) {
	return schemaHost(h).SearchNamespaces(a, name, class)
}
func (h typedefHost) LookupFunction(a ddl.Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (*catalog.Procedure, error) {
	return schemaHost(h).LookupFunction(a, name, signature)
}
func (h typedefHost) DependencyObject(a ddl.Authority, address ids.ObjectAddress) (string, error) {
	return schemaHost(h).DependencyObject(a, address)
}

func (h typedefHost) TrustedRestoreDestination(a ddl.Authority, what string) (bool, error) {
	if h.op == nil || !operationReplayOrZero(h.op).tableDestination {
		return false, nil
	}
	auth := ddlAuthorityOf(a)
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser || h.e.importTargets == nil {
		return true, fmt.Errorf("stuntgres: invalid trusted restore %s authority", what)
	}
	return true, nil
}

func (h typedefHost) AllocateEnumMember() (int64, error) {
	return h.e.catalog.allocateEnumMemberLocked()
}

func (h typedefHost) ReplaceUnsynchronized(d catalog.Descriptor) error {
	return h.stage.replaceDescriptor(d, false)
}

func (h typedefHost) CreationTypeACL(owner authorization.PrincipalRef, schema ids.ObjectAddress) (authorization.ACLValue, bool) {
	return h.stage.creationTypeACL(owner, schema)
}

func (h typedefHost) AddDomainBase(d catalog.Descriptor) {
	h.stage.domainBases = append(h.stage.domainBases, d)
}

func (h typedefHost) RemoveDomainCheck(d *catalog.Type, oid ids.OID) error {
	return h.stage.removeDomainCheck(d, oid)
}

func (h typedefHost) NamedType(a ddl.Authority, name parser.ObjectName) (*catalog.Type, error) {
	return h.e.namedType(ddlAuthorityOf(a), name)
}

func (h typedefHost) FindCollation(a ddl.Authority, name parser.ObjectName) (*catalog.Collation, error) {
	return h.e.findCollation(ddlAuthorityOf(a), name)
}

func (h typedefHost) ResolveType(raw parser.TypeName) (sqltype.Usage, error) {
	return h.e.resolveType(raw, h.op)
}

func (h typedefHost) ResolveDomainBase(raw parser.TypeName) (sqltype.Usage, error) {
	return h.e.resolveAnnotationTypeMode(raw, h.op, false, true)
}

func (h typedefHost) ResolveColumnCollation(u sqltype.Usage, raw *parser.ObjectName) (sqltype.Usage, error) {
	return h.e.resolveColumnCollation(u, raw, h.op)
}

func (h typedefHost) BindDomainDefault(a ddl.Authority, d *catalog.Type, raw parser.Expr, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
	return h.e.bindDomainDefault(h.op.Context(), h.op, ddlAuthorityOf(a), h.stage, d, raw, input, output)
}

func (h typedefHost) BindDomainChecks(a ddl.Authority, d *catalog.Type, raw []parser.ColumnConstraint, input sqltype.InputEnvironment) error {
	return h.e.bindDomainChecks(h.op, ddlAuthorityOf(a), h.stage, d, raw, input)
}

func (h typedefHost) DisplaceAssociatedArray(namespace ids.ObjectAddress, name string) (bool, error) {
	return h.e.displaceAssociatedArrayLocked(h.stage, namespace, name)
}

func (h typedefHost) AssociatedArrayName(namespace ids.ObjectAddress, name string) string {
	return associatedArrayName(h.stage, namespace, name)
}

func (h typedefHost) NewCompositeRelation(namespace ids.ObjectAddress, name string, owner authorization.PrincipalRef, columns []catalog.Column, index map[string]int) (*catalog.Relation, error) {
	relation, err := h.e.newTableDescriptorLocked(namespace, name, owner, columns, index)
	if err != nil {
		return nil, err
	}
	relation.RelationKind = catalog.CompositeRelation
	return relation, nil
}

func (h typedefHost) StageRowType(relation *catalog.Relation) error {
	return h.e.stageRowTypeLocked(h.stage, relation)
}
