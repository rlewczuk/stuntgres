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

// ddlHost is the one root staging adapter: it implements ddl.Host here,
// routine.DDLHost in routine_host.go and typedef.StageHost in
// typedef_host.go, for one DDL statement. Staging runs under Engine.mu without
// a StatementScope, so it holds the engine, the operation and the catalog
// stage, and no authorization view: the handlers pass their authority in.
// Like extensionHost it wraps the stage and is never a defined type over
// CatalogStage, so Create and Replace keep the stage's lifecycle checks.
type ddlHost struct {
	e     *Engine
	op    *session.Operation
	stage *CatalogStage
}

var _ ddl.Host = ddlHost{}

// DDLAuthority marks AuthorizationView as a ddl.Authority.
func (AuthorizationView) DDLAuthority() {}

// ddlAuthorityOf recovers the view behind a. The handlers only carry
// authorities the engine gave them; meeting another kind is a bug.
func ddlAuthorityOf(a ddl.Authority) AuthorizationView {
	v, ok := a.(AuthorizationView)
	if !ok {
		panic(fmt.Sprintf("stuntgres: unexpected ddl authority %T", a))
	}
	return v
}

func (h ddlHost) AccessView(a ddl.Authority) access.View { return ddlAuthorityOf(a).View }

func (h ddlHost) Recapture(a ddl.Authority, view catalog.Read) ddl.Authority {
	v := ddlAuthorityOf(a)
	v.Recapture(view)
	return v
}

func (h ddlHost) CreationNamespace(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, ddl.Authority, error) {
	v := ddlAuthorityOf(a)
	ref, err := h.e.collationCreationNamespaceLocked(&v, h.stage, name)
	return ref, v, err
}

func (h ddlHost) CheckCreateIn(a ddl.Authority, namespace ids.ObjectRef) error {
	return h.e.conversionCheckCreate(h.op, ddlAuthorityOf(a), namespace)
}

func (h ddlHost) ExistingCreationNamespace(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.e.creationNamespace(ddlAuthorityOf(a), name)
}

func (h ddlHost) SearchNamespaces(a ddl.Authority, name parser.ObjectName, class ids.OID) ([]ids.ObjectAddress, error) {
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
	path, err := h.e.lookupNamespaces(ddlAuthorityOf(a), name, kind)
	if err != nil {
		return nil, err
	}
	namespaces := make([]ids.ObjectAddress, path.Count())
	for i := range namespaces {
		namespaces[i] = path.At(i)
	}
	return namespaces, nil
}

func (h ddlHost) LookupFunction(a ddl.Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (*catalog.Procedure, error) {
	v := ddlAuthorityOf(a)
	path, err := h.e.lookupNamespaces(v, name, analyze.FunctionLookup)
	if err != nil {
		return nil, err
	}
	return analyze.ProcedureInNamespaces(v.Catalog, path, name.Parts[len(name.Parts)-1].Text, signature), nil
}

// DependencyObject renders the description even when the path fails, so a
// caller that only wants the text keeps it.
func (h ddlHost) DependencyObject(a ddl.Authority, address ids.ObjectAddress) (string, error) {
	v := ddlAuthorityOf(a)
	path, err := referencePath(v)
	return stored.DependencyObject(v.Catalog, address, path), err
}

func (h ddlHost) Stage() ddl.Stage        { return h.stage }
func (h ddlHost) StageBase() catalog.Read { return h.stage.Base }
func (h ddlHost) StageRead() catalog.Read { return h.e.stageRead(h.stage) }

func (h ddlHost) CreationACL(owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	return h.stage.CreationACL(owner, schema, class)
}

func (h ddlHost) DropTarget(address ids.ObjectAddress) {
	h.stage.domainTargets = append(h.stage.domainTargets, address)
}

func (h ddlHost) RecordDependencyPath(a ddl.Authority) error {
	var err error
	h.stage.dependencyPath, err = referencePath(ddlAuthorityOf(a))
	return err
}

func (h ddlHost) AllocateOID() (ids.OID, error)             { return h.e.catalog.AllocateOIDLocked() }
func (h ddlHost) Revision() (ids.DefinitionRevision, error) { return h.e.catalog.RevisionLocked() }

func (h ddlHost) Context() context.Context { return h.op.Context() }

func (h ddlHost) EmitNotice(level session.NoticeLevel, diagnostic sqlstate.Error) {
	h.op.EmitNotice(level, diagnostic)
}
