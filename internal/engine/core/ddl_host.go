package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// DDLHost is the core staging adapter for one DDL statement: ddl.Host, plus
// what core paths call (LookupCollation for index.KeyHost, NamedType for
// table.TypedResolver, OtherDatabaseDependencies) and the methods that
// families with no arrow between them share (AddDomainBase, RemoveObject,
// RelationDiagnostic, ResolveRelation, SelectFunction). Each family host
// embeds it, so every host has this {E, Op, CatalogStage} layout:
// TableHost (table_host.go), TypeHost (typedef_host.go), ViewHost
// (view_host.go), RoutineHost (routine_host.go), RoleHost (role_host.go)
// and SchemaHost (schema_host.go). Family code builds its own host.
// Staging enters and returns under Engine.Mu and holds the engine, operation
// and catalog stage, but no authorization view: handlers pass their authority
// in. ValidateRoutineSetting alone suspends staging for external Normalize,
// releasing Engine.Mu and the active statement's replacement reservation before
// exact live readmission. Like extensionHost it wraps the stage and
// is never a defined type over CatalogStage, so Create and Replace keep the
// stage's lifecycle checks.
type DDLHost struct {
	E            *Engine
	Op           *session.Operation
	CatalogStage *CatalogStage
}

var (
	_ ddl.Host              = DDLHost{}
	_ ddl.ConfigurationHost = DDLHost{}
	_ ddl.Allocator         = DDLHost{}
	_ table.TypedResolver   = DDLHost{}
)

// DDLAuthority marks AuthorizationView as a ddl.Authority.
func (AuthorizationView) DDLAuthority() {}

// DDLAuthorityOf recovers the view behind a. The handlers only carry
// authorities the engine gave them; meeting another kind is a bug.
func DDLAuthorityOf(a ddl.Authority) AuthorizationView {
	v, ok := a.(AuthorizationView)
	if !ok {
		panic(fmt.Sprintf("stuntgres: unexpected ddl authority %T", a))
	}
	return v
}

func (h DDLHost) AccessView(a ddl.Authority) access.View { return DDLAuthorityOf(a).View }

func (h DDLHost) Recapture(a ddl.Authority, view catalog.Read) ddl.Authority {
	v := DDLAuthorityOf(a)
	v.Recapture(view)
	return v
}

func (h DDLHost) CreationNamespace(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, ddl.Authority, error) {
	v := DDLAuthorityOf(a)
	ref, err := h.E.collationCreationNamespaceLocked(&v, h.CatalogStage, name)
	return ref, v, err
}

func (h DDLHost) CheckCreateIn(a ddl.Authority, namespace ids.ObjectRef) error {
	return h.E.conversionCheckCreate(h.Op, DDLAuthorityOf(a), namespace)
}

func (h DDLHost) ExistingCreationNamespace(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.E.CreationNamespace(DDLAuthorityOf(a), name)
}

func (h DDLHost) SearchNamespaces(a ddl.Authority, name parser.ObjectName, class ids.OID) ([]ids.ObjectAddress, error) {
	var kind analyze.LookupKind
	switch class {
	case ids.TypeClassID:
		kind = analyze.TypeLookup
	case ids.ConversionClassID, ids.StatisticExtClassID:
		kind = analyze.ConversionLookup
	case ids.OperatorClassID, ids.OperatorFamilyClassID, ids.OperatorClassCatalogID, ids.TSTemplateClassID, ids.TSDictionaryClassID, ids.TSConfigurationClassID, 3601:
		kind = analyze.OperatorLookup
	case ids.ProcedureClassID:
		kind = analyze.FunctionLookup
	default:
		return nil, fmt.Errorf("stuntgres: no namespace search for class %d", class)
	}
	path, err := h.E.LookupNamespaces(DDLAuthorityOf(a), name, kind)
	if err != nil {
		return nil, err
	}
	namespaces := make([]ids.ObjectAddress, path.Count())
	for i := range namespaces {
		namespaces[i] = path.At(i)
	}
	return namespaces, nil
}

// SearchConfigurationNamespaces applies configuration utility source policy
// without changing generic dictionary, parser or expression lookup.
func (h DDLHost) SearchConfigurationNamespaces(a ddl.Authority, name parser.ObjectName, class ids.OID) ([]ids.ObjectAddress, error) {
	auth := DDLAuthorityOf(a)
	// Trusted Restore destination construction grants no authority over an
	// existing source operand, including dictionaries and parsers.
	auth.restoreDefinitionLookup = false
	var semanticParts [2]parser.Identifier
	if len(name.Parts) == len(semanticParts) {
		copy(semanticParts[:], name.Parts)
		// Utility resolution has no expression location. Never edit the AST.
		semanticParts[0].Pos = parser.Position{}
		name.Parts = semanticParts[:]
	}
	return h.SearchNamespaces(auth, name, class)
}

func (h DDLHost) LookupFunction(a ddl.Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (*catalog.Procedure, error) {
	v := DDLAuthorityOf(a)
	path, err := h.E.LookupNamespaces(v, name, analyze.FunctionLookup)
	if err != nil {
		return nil, err
	}
	return analyze.ProcedureInNamespaces(v.Catalog, path, name.Parts[len(name.Parts)-1].Text, signature), nil
}

// DependencyObject renders the description even when the path fails, so a
// caller that only wants the text keeps it.
func (h DDLHost) DependencyObject(a ddl.Authority, address ids.ObjectAddress) (string, error) {
	v := DDLAuthorityOf(a)
	path, err := ReferencePath(v)
	return stored.DependencyObject(v.Catalog, address, path), err
}

func (h DDLHost) Stage() ddl.Stage        { return h.CatalogStage }
func (h DDLHost) StageBase() catalog.Read { return h.CatalogStage.Base }
func (h DDLHost) StageRead() catalog.Read { return h.E.StageRead(h.CatalogStage) }

func (h DDLHost) CreationACL(owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	return h.CatalogStage.CreationACL(owner, schema, class)
}

func (h DDLHost) DropTarget(address ids.ObjectAddress) {
	h.CatalogStage.MarkDrop(address, catalog.DropProvenance{Original: true})
	h.CatalogStage.DomainTargets = append(h.CatalogStage.DomainTargets, address)
}

func (h DDLHost) RecordDependencyPath(a ddl.Authority) error {
	var err error
	h.CatalogStage.DependencyPath, err = ReferencePath(DDLAuthorityOf(a))
	return err
}

func (h DDLHost) AllocateOID() (ids.OID, error)             { return h.E.Catalog.AllocateOIDLocked() }
func (h DDLHost) Revision() (ids.DefinitionRevision, error) { return h.E.Catalog.RevisionLocked() }

func (h DDLHost) Context() context.Context { return h.Op.Context() }

func (h DDLHost) EmitNotice(level session.NoticeLevel, diagnostic sqlstate.Error) {
	h.Op.EmitNotice(level, diagnostic)
}

func (h DDLHost) LookupCollation(raw parser.ObjectName) (uint32, error) {
	return h.E.lookupCollation(raw, h.Op)
}

func (h DDLHost) AddDomainBase(d catalog.Descriptor) {
	h.CatalogStage.DomainBases = append(h.CatalogStage.DomainBases, d)
}

func (h DDLHost) NamedType(a ddl.Authority, name parser.ObjectName) (*catalog.Type, error) {
	return h.E.namedType(DDLAuthorityOf(a), name)
}

func (h DDLHost) ResolveRelation(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.E.ResolveRelationReference(DDLAuthorityOf(a), name, true)
}

func (h DDLHost) OtherDatabaseDependencies(ref authorization.PrincipalRef) []access.RoleDependency {
	var result []access.RoleDependency
	for _, target := range h.E.Cluster.Targets {
		if target == h.E {
			continue
		}
		var dependencies []access.RoleDependency
		for address, descriptor := range target.Catalog.State.Descriptors() {
			if address.ClassID == ids.SubscriptionClassID {
				continue
			}
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

func (h DDLHost) SelectFunction(a ddl.Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (catalog.RoutineIdentity, int, error) {
	v := DDLAuthorityOf(a)
	path, err := h.E.LookupNamespaces(v, name, analyze.FunctionLookup)
	if err != nil {
		return catalog.RoutineIdentity{}, 0, err
	}
	identity, count := regobj.SelectRoutine(v.Catalog, path, name.Parts[len(name.Parts)-1].Text, &signature)
	return identity, count, nil
}

func (h DDLHost) RemoveObject(ref ids.ObjectRef, namespace bool) error {
	if d := h.CatalogStage.Base.Get(ref.Address); d != nil && h.CatalogStage.DropFlags[d.Namespace()].Original {
		h.CatalogStage.MarkDrop(ref.Address, catalog.DropProvenance{Normal: true})
	}
	return h.CatalogStage.RemoveObjectLocked(ref, namespace)
}

func (h DDLHost) RelationDiagnostic(a ddl.Authority, relation *catalog.Relation) (kind, name string) {
	return catalog.RelationDiagnosticKind(relation), relationDiagnosticName(DDLAuthorityOf(a), relation)
}
