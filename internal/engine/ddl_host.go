package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// DDLHost is the one root staging adapter: it implements ddl.Host here,
// routine.DDLHost in routine_host.go and typedef.StageHost in
// typedef_host.go, for one DDL statement. Staging runs under Engine.mu without
// a StatementScope, so it holds the engine, the operation and the catalog
// stage, and no authorization view: the handlers pass their authority in.
// Like extensionHost it wraps the stage and is never a defined type over
// CatalogStage, so Create and Replace keep the stage's lifecycle checks.
type DDLHost struct {
	E            *Engine
	Op           *session.Operation
	CatalogStage *CatalogStage
}

var _ ddl.Host = DDLHost{}

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
	case ids.ConversionClassID:
		kind = analyze.ConversionLookup
	case ids.OperatorClassID, ids.OperatorFamilyClassID, ids.OperatorClassCatalogID:
		kind = analyze.OperatorLookup
	case ids.ProcedureClassID:
		kind = analyze.FunctionLookup
	default:
		return nil, fmt.Errorf("stuntgres: no namespace search for class %d", class)
	}
	path, err := h.E.lookupNamespaces(DDLAuthorityOf(a), name, kind)
	if err != nil {
		return nil, err
	}
	namespaces := make([]ids.ObjectAddress, path.Count())
	for i := range namespaces {
		namespaces[i] = path.At(i)
	}
	return namespaces, nil
}

func (h DDLHost) LookupFunction(a ddl.Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (*catalog.Procedure, error) {
	v := DDLAuthorityOf(a)
	path, err := h.E.lookupNamespaces(v, name, analyze.FunctionLookup)
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
