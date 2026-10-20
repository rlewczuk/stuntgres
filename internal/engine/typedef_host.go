package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// typedefHost is schemaHost seen as typedef.StageHost. It is a separate type
// because the feature packages' hosts have methods with the same names but
// their own authority and stage types.
type typedefHost schemaHost

var _ typedef.StageHost = typedefHost{}

// TypedefAuthority marks AuthorizationView as a typedef.Authority.
func (AuthorizationView) TypedefAuthority() {}

// typedefAuthorityOf recovers the view behind a. The handlers only carry
// authorities the engine gave them; meeting another kind is a bug.
func typedefAuthorityOf(a typedef.Authority) AuthorizationView {
	v, ok := a.(AuthorizationView)
	if !ok {
		panic(fmt.Sprintf("stuntgres: unexpected typedef authority %T", a))
	}
	return v
}

func (h typedefHost) AccessView(a typedef.Authority) access.View { return typedefAuthorityOf(a).View }

func (h typedefHost) CheckCreateIn(a typedef.Authority, namespace ids.ObjectRef) error {
	return h.e.conversionCheckCreate(h.op, typedefAuthorityOf(a), namespace)
}

func (h typedefHost) TrustedRestoreDestination(a typedef.Authority, what string) (bool, error) {
	if h.op == nil || !operationReplayOrZero(h.op).tableDestination {
		return false, nil
	}
	auth := typedefAuthorityOf(a)
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser || h.e.importTargets == nil {
		return true, fmt.Errorf("stuntgres: invalid trusted restore %s authority", what)
	}
	return true, nil
}

func (h typedefHost) Context() context.Context { return h.op.Context() }

func (h typedefHost) EmitNotice(level session.NoticeLevel, diagnostic sqlstate.Error) {
	h.op.EmitNotice(level, diagnostic)
}

func (h typedefHost) AllocateOID() (ids.OID, error)             { return h.e.catalog.allocateOIDLocked() }
func (h typedefHost) Revision() (ids.DefinitionRevision, error) { return h.e.catalog.revisionLocked() }
func (h typedefHost) AllocateEnumMember() (int64, error) {
	return h.e.catalog.allocateEnumMemberLocked()
}

func (h typedefHost) Stage() typedef.Stage    { return h.stage }
func (h typedefHost) StageRead() catalog.Read { return h.e.stageRead(h.stage) }

func (h typedefHost) ReplaceUnsynchronized(d catalog.Descriptor) error {
	return h.stage.replaceDescriptor(d, false)
}

func (h typedefHost) CreationTypeACL(owner authorization.PrincipalRef, schema ids.ObjectAddress) (authorization.ACLValue, bool) {
	return h.stage.creationTypeACL(owner, schema)
}

func (h typedefHost) AddDomainBase(d catalog.Descriptor) {
	h.stage.domainBases = append(h.stage.domainBases, d)
}

func (h typedefHost) DropTarget(address ids.ObjectAddress) {
	h.stage.domainTargets = append(h.stage.domainTargets, address)
}

func (h typedefHost) RecordDependencyPath(a typedef.Authority) {
	h.stage.dependencyPath, _ = h.e.referencePath(typedefAuthorityOf(a))
}

func (h typedefHost) RemoveDomainCheck(d *catalog.Type, oid ids.OID) error {
	return h.stage.removeDomainCheck(d, oid)
}

func (h typedefHost) CreationNamespace(a typedef.Authority, name parser.ObjectName) (ids.ObjectRef, typedef.Authority, error) {
	v := typedefAuthorityOf(a)
	ref, err := h.e.collationCreationNamespaceLocked(&v, h.stage, name)
	return ref, v, err
}

func (h typedefHost) ExistingCreationNamespace(a typedef.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.e.creationNamespace(typedefAuthorityOf(a), name)
}

func (h typedefHost) SearchNamespaces(a typedef.Authority, name parser.ObjectName, class ids.OID) ([]ids.ObjectAddress, error) {
	var kind analyze.LookupKind
	switch class {
	case ids.TypeClassID:
		kind = analyze.TypeLookup
	case ids.ConversionClassID:
		kind = analyze.ConversionLookup
	default:
		return nil, fmt.Errorf("stuntgres: no namespace search for class %d", class)
	}
	path, err := h.e.lookupNamespaces(typedefAuthorityOf(a), name, kind)
	if err != nil {
		return nil, err
	}
	namespaces := make([]ids.ObjectAddress, path.Count())
	for i := range namespaces {
		namespaces[i] = path.At(i)
	}
	return namespaces, nil
}

func (h typedefHost) LookupFunction(a typedef.Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (*catalog.Procedure, error) {
	v := typedefAuthorityOf(a)
	path, err := h.e.lookupNamespaces(v, name, analyze.FunctionLookup)
	if err != nil {
		return nil, err
	}
	return analyze.ProcedureInNamespaces(v.Catalog, path, name.Parts[len(name.Parts)-1].Text, signature), nil
}

func (h typedefHost) NamedType(a typedef.Authority, name parser.ObjectName) (*catalog.Type, error) {
	return h.e.namedType(typedefAuthorityOf(a), name)
}

func (h typedefHost) FindCollation(a typedef.Authority, name parser.ObjectName) (*catalog.Collation, error) {
	return h.e.findCollation(typedefAuthorityOf(a), name)
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

func (h typedefHost) BindDomainDefault(a typedef.Authority, d *catalog.Type, raw parser.Expr, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
	return h.e.bindDomainDefault(h.op.Context(), h.op, typedefAuthorityOf(a), h.stage, d, raw, input, output)
}

func (h typedefHost) BindDomainChecks(a typedef.Authority, d *catalog.Type, raw []parser.ColumnConstraint, input sqltype.InputEnvironment) error {
	return h.e.bindDomainChecks(h.op, typedefAuthorityOf(a), h.stage, d, raw, input)
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

func (h typedefHost) DependencyObject(a typedef.Authority, address ids.ObjectAddress) (string, error) {
	v := typedefAuthorityOf(a)
	path, err := h.e.referencePath(v)
	if err != nil {
		return "", err
	}
	return dependencyObject(v.Catalog, address, path), nil
}
