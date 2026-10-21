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
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// schemaHost is the root adapter behind ddl.Host and routine.DDLHost (and,
// as typedefHost, typedef.StageHost) for one schema statement. Schema
// staging runs under Engine.mu without a StatementScope, so it holds the
// engine, the operation and the catalog stage, and no authorization view: the
// handlers pass their authority in.
type schemaHost struct {
	e     *Engine
	op    *session.Operation
	stage *CatalogStage
}

var (
	_ ddl.Host        = schemaHost{}
	_ routine.DDLHost = schemaHost{}
)

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

func (h schemaHost) AccessView(a ddl.Authority) access.View { return ddlAuthorityOf(a).View }

func (h schemaHost) Recapture(a ddl.Authority, view catalog.Read) ddl.Authority {
	v := ddlAuthorityOf(a)
	v.Recapture(view)
	return v
}

func (h schemaHost) DefinitionType(a ddl.Authority, id sqltype.ID) (*sqltype.Descriptor, bool) {
	return ddlAuthorityOf(a).definitionType(id)
}

func (h schemaHost) DefinitionLookup(a ddl.Authority, key ids.NameKey) ids.ObjectAddress {
	return ddlAuthorityOf(a).definitionLookup(key)
}

func (h schemaHost) CreationNamespace(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, ddl.Authority, error) {
	v := ddlAuthorityOf(a)
	ref, err := h.e.collationCreationNamespaceLocked(&v, h.stage, name)
	return ref, v, err
}

func (h schemaHost) CheckCreateIn(a ddl.Authority, namespace ids.ObjectRef) error {
	return h.e.conversionCheckCreate(h.op, ddlAuthorityOf(a), namespace)
}

func (h schemaHost) ExistingCreationNamespace(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.e.creationNamespace(ddlAuthorityOf(a), name)
}

func (h schemaHost) SearchNamespaces(a ddl.Authority, name parser.ObjectName, class ids.OID) ([]ids.ObjectAddress, error) {
	var kind analyze.LookupKind
	switch class {
	case ids.TypeClassID:
		kind = analyze.TypeLookup
	case ids.ConversionClassID:
		kind = analyze.ConversionLookup
	case ids.OperatorClassID:
		kind = analyze.OperatorLookup
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

func (h schemaHost) LookupFunction(a ddl.Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (*catalog.Procedure, error) {
	v := ddlAuthorityOf(a)
	path, err := h.e.lookupNamespaces(v, name, analyze.FunctionLookup)
	if err != nil {
		return nil, err
	}
	return analyze.ProcedureInNamespaces(v.Catalog, path, name.Parts[len(name.Parts)-1].Text, signature), nil
}

func (h schemaHost) SelectFunction(a ddl.Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (catalog.RoutineIdentity, int, error) {
	v := ddlAuthorityOf(a)
	path, err := h.e.lookupNamespaces(v, name, analyze.FunctionLookup)
	if err != nil {
		return catalog.RoutineIdentity{}, 0, err
	}
	identity, count := selectRoutine(v.Catalog, path, name.Parts[len(name.Parts)-1].Text, &signature)
	return identity, count, nil
}

func (h schemaHost) ResolveRegtype(a ddl.Authority, raw parser.TypeName) (ids.OID, error) {
	input, err := h.e.captureRegtypeInput(ddlAuthorityOf(a), h.op)
	if err != nil {
		return 0, err
	}
	id, err := input.resolveParsedTypeName(h.op.Context(), raw, h.op)
	return ids.OID(id), err
}

// DependencyObject renders the description even when the path fails, so a
// caller that only wants the text keeps it.
func (h schemaHost) DependencyObject(a ddl.Authority, address ids.ObjectAddress) (string, error) {
	v := ddlAuthorityOf(a)
	path, err := h.e.referencePath(v)
	return dependencyObject(v.Catalog, address, path), err
}

func (h schemaHost) Stage() ddl.Stage        { return h.stage }
func (h schemaHost) StageBase() catalog.Read { return h.stage.Base }
func (h schemaHost) StageRead() catalog.Read { return h.e.stageRead(h.stage) }

func (h schemaHost) CreationACL(owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	return h.stage.creationACL(owner, schema, class)
}

func (h schemaHost) DropTarget(address ids.ObjectAddress) {
	h.stage.domainTargets = append(h.stage.domainTargets, address)
}

func (h schemaHost) RecordDependencyPath(a ddl.Authority) {
	h.stage.dependencyPath, _ = h.e.referencePath(ddlAuthorityOf(a))
}

func (h schemaHost) AllocateOID() (ids.OID, error)             { return h.e.catalog.allocateOIDLocked() }
func (h schemaHost) Revision() (ids.DefinitionRevision, error) { return h.e.catalog.revisionLocked() }

func (h schemaHost) Context() context.Context { return h.op.Context() }

func (h schemaHost) EmitNotice(level session.NoticeLevel, diagnostic sqlstate.Error) {
	h.op.EmitNotice(level, diagnostic)
}

// BindSQLRoutine binds d's body with the stage's definitions in place of the
// operation's. Restore defers SQL body validation until the import ends.
func (h schemaHost) BindSQLRoutine(a ddl.Authority, d *catalog.Procedure, input sqltype.InputEnvironment) error {
	if operationReplayOrZero(h.op).sqlBodies {
		return nil
	}
	previous := h.op.Definition
	h.op.Definition = stageDefinitionReader{h.stage}
	defer func() { h.op.Definition = previous }()
	auth := ddlAuthorityOf(a)
	auth.definition = h.op.Definition
	_, _, err := h.e.bindSQLRoutineLocked(h.op, auth, d, input, nil)
	return err
}
