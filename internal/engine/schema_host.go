package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// schemaHost is the root adapter behind routine.DDLHost (and, as
// typedefHost, typedef.StageHost) for one schema statement. Schema staging runs under Engine.mu without a StatementScope, so
// it holds the engine, the operation and the catalog stage, and no
// authorization view: the handlers pass their authority in.
type schemaHost struct {
	e     *Engine
	op    *session.Operation
	stage *CatalogStage
}

var _ routine.DDLHost = schemaHost{}

// RoutineAuthority marks AuthorizationView as a routine.Authority.
func (AuthorizationView) RoutineAuthority() {}

// routineAuthorityOf recovers the view behind a. The handlers only carry
// authorities the engine gave them; meeting another kind is a bug.
func routineAuthorityOf(a routine.Authority) AuthorizationView {
	v, ok := a.(AuthorizationView)
	if !ok {
		panic(fmt.Sprintf("stuntgres: unexpected routine authority %T", a))
	}
	return v
}

func (h schemaHost) AccessView(a routine.Authority) access.View { return routineAuthorityOf(a).View }

func (h schemaHost) Recapture(a routine.Authority, view catalog.Read) routine.Authority {
	v := routineAuthorityOf(a)
	v.Recapture(view)
	return v
}

func (h schemaHost) DefinitionType(a routine.Authority, id sqltype.ID) (*sqltype.Descriptor, bool) {
	return routineAuthorityOf(a).definitionType(id)
}

func (h schemaHost) DefinitionLookup(a routine.Authority, key ids.NameKey) ids.ObjectAddress {
	return routineAuthorityOf(a).definitionLookup(key)
}

func (h schemaHost) CreationNamespace(a routine.Authority, name parser.ObjectName) (ids.ObjectRef, routine.Authority, error) {
	v := routineAuthorityOf(a)
	ref, err := h.e.collationCreationNamespaceLocked(&v, h.stage, name)
	return ref, v, err
}

func (h schemaHost) CheckCreateIn(a routine.Authority, namespace ids.ObjectRef) error {
	return h.e.conversionCheckCreate(h.op, routineAuthorityOf(a), namespace)
}

func (h schemaHost) OperatorNamespaces(a routine.Authority, name parser.ObjectName) ([]ids.ObjectAddress, error) {
	path, err := h.e.lookupNamespaces(routineAuthorityOf(a), name, analyze.OperatorLookup)
	if err != nil {
		return nil, err
	}
	namespaces := make([]ids.ObjectAddress, path.Count())
	for i := range namespaces {
		namespaces[i] = path.At(i)
	}
	return namespaces, nil
}

func (h schemaHost) LookupFunction(a routine.Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (*catalog.Procedure, error) {
	v := routineAuthorityOf(a)
	path, err := h.e.lookupNamespaces(v, name, analyze.FunctionLookup)
	if err != nil {
		return nil, err
	}
	return analyze.ProcedureInNamespaces(v.Catalog, path, name.Parts[len(name.Parts)-1].Text, signature), nil
}

func (h schemaHost) SelectFunction(a routine.Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (catalog.RoutineIdentity, int, error) {
	v := routineAuthorityOf(a)
	path, err := h.e.lookupNamespaces(v, name, analyze.FunctionLookup)
	if err != nil {
		return catalog.RoutineIdentity{}, 0, err
	}
	identity, count := selectRoutine(v.Catalog, path, name.Parts[len(name.Parts)-1].Text, &signature)
	return identity, count, nil
}

func (h schemaHost) ResolveType(a routine.Authority, raw parser.TypeName) (ids.OID, error) {
	input, err := h.e.captureRegtypeInput(routineAuthorityOf(a), h.op)
	if err != nil {
		return 0, err
	}
	id, err := input.resolveParsedTypeName(h.op.Context(), raw, h.op)
	return ids.OID(id), err
}

func (h schemaHost) DependencyObject(a routine.Authority, address ids.ObjectAddress) string {
	v := routineAuthorityOf(a)
	path, _ := h.e.referencePath(v)
	return dependencyObject(v.Catalog, address, path)
}

func (h schemaHost) Stage() routine.Stage    { return h.stage }
func (h schemaHost) StageBase() catalog.Read { return h.stage.Base }
func (h schemaHost) StageRead() catalog.Read { return h.e.stageRead(h.stage) }

func (h schemaHost) CreationACL(owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	return h.stage.creationACL(owner, schema, class)
}

func (h schemaHost) DropTarget(address ids.ObjectAddress) {
	h.stage.domainTargets = append(h.stage.domainTargets, address)
}

func (h schemaHost) RecordDependencyPath(a routine.Authority) {
	h.stage.dependencyPath, _ = h.e.referencePath(routineAuthorityOf(a))
}

func (h schemaHost) AllocateOID() (ids.OID, error)             { return h.e.catalog.allocateOIDLocked() }
func (h schemaHost) Revision() (ids.DefinitionRevision, error) { return h.e.catalog.revisionLocked() }

func (h schemaHost) EmitNotice(level session.NoticeLevel, diagnostic sqlstate.Error) {
	h.op.EmitNotice(level, diagnostic)
}

// BindSQLRoutine binds d's body with the stage's definitions in place of the
// operation's. Restore defers SQL body validation until the import ends.
func (h schemaHost) BindSQLRoutine(a routine.Authority, d *catalog.Procedure, input sqltype.InputEnvironment) error {
	if operationReplayOrZero(h.op).sqlBodies {
		return nil
	}
	previous := h.op.Definition
	h.op.Definition = stageDefinitionReader{h.stage}
	defer func() { h.op.Definition = previous }()
	auth := routineAuthorityOf(a)
	auth.definition = h.op.Definition
	_, _, err := h.e.bindSQLRoutineLocked(h.op, auth, d, input, nil)
	return err
}
