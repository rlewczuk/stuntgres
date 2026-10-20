package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// analyzeHost is what statement binding may ask of the engine: the binder
// reaches engine, session and operation state only through it. Each method
// is a reviewed capability (ENGINE-SPLIT-analyze.md, 12c). Binding runs with
// Engine.mu held; every method is called with Engine.mu held and never takes
// or releases it.
type analyzeHost interface {
	// Authority.
	Authorization() (AuthorizationView, error)
	Definition() definitionReader
	AuthorizationStorage(auth AuthorizationView, address ids.ObjectAddress) *storage.Relation
	AdmittedReference(auth AuthorizationView, name parser.ObjectName, ref ids.ObjectRef) (ids.ObjectRef, error)
	ResolveReference(auth AuthorizationView, name parser.ObjectName) (ids.ObjectRef, error)

	// Catalog.
	CatalogOwner() *catalog.Owner
	CurrentCatalog() catalog.Read

	// Names.
	LookupNamespaces(auth AuthorizationView, name parser.ObjectName, kind lookupKind) (namespaceSelection, error)
	LookupNamespacesSnapshot(auth AuthorizationView, name parser.ObjectName, kind lookupKind) (namespaceSelection, error)
	LookupPath(auth AuthorizationView, kind lookupKind) ([]ids.ObjectAddress, error)
	ReferencePath(auth AuthorizationView) ([]ids.ObjectAddress, error)
	Regclass(auth AuthorizationView, text string) (ids.ObjectRef, error)
	LookupCollation(raw parser.ObjectName) (uint32, error)
	LookupIndexClass(auth AuthorizationView, raw parser.ObjectName, method catalog.IndexMethod) (sqltype.IndexClass, error)
	NamedType(auth AuthorizationView, name parser.ObjectName) (*catalog.Type, error)

	// Types.
	ResolveType(raw parser.TypeName) (sqltype.Usage, error)
	ResolveAnnotationType(raw parser.TypeName, unknownNull bool) (sqltype.Usage, error)
	ResolveStringAnnotationType(raw parser.TypeName, unknownNull bool) (sqltype.Usage, error)
	ResolveAnnotationTypeInput(raw parser.TypeName, unknownNull, domainBase, stringInput, jsonReturning, thresholdArrayInput bool) (sqltype.Usage, error)
	ResolveTableColumnType(raw parser.TypeName) (sqltype.Usage, error)
	ResolveColumnCollation(u sqltype.Usage, raw *parser.ObjectName) (sqltype.Usage, error)

	// Captures.
	CaptureElementInput(auth AuthorizationView, input *sqltype.InputEnvironment) error
	CaptureOperatorInput(auth AuthorizationView) (capturedOperatorInput, error)
	CaptureRegtypeInput(auth AuthorizationView) (capturedRegtypeInput, error)
	CaptureTypeDiagnosticInput(auth AuthorizationView, input *sqltype.InputEnvironment, typ *sqltype.Descriptor) error
	CaptureTypeDiagnosticName(auth AuthorizationView) (func(sqltype.ID) string, error)
	CaptureEnumOperationInput(auth AuthorizationView, input *sqltype.InputEnvironment) error
	CaptureEnumOperationSafety(input *sqltype.InputEnvironment)
	FTSInput() sqltype.FTSInputNotices

	// Session.
	PlanOwner() (owner ids.SessionToken, discardGeneration uint64, ok bool)

	// Effects.
	EmitWarning(diagnostic sqlstate.Error)
	EmitNotice(diagnostic sqlstate.Error)
	ReplayInsertAuthority() bool
	ReplayDomainCell(b *binding, s *parser.Insert, row, column int) (expr.Expr, bool, error)

	// Upward.
	DecodeDomainLiteral(ctx context.Context, auth *AuthorizationView, read catalog.Read, typ *sqltype.Descriptor, text string, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error)
	IndexExpressionBinder(input sqltype.InputEnvironment, auth AuthorizationView, parent *catalog.Relation) *expressionBinder
	BindViewWrite(b *binding, auth AuthorizationView, stmt parser.Statement, consumed ...ids.ObjectRef) error
}

// bindingHost is the root adapter behind analyzeHost. Binders also run
// without a StatementScope (DDL, defaults, checks, index expressions,
// generated columns, Restore), so it holds the engine and the operation
// rather than a scope. A nil op means no definition reader, no notices, no
// plan owner and no Restore replay, as before the seam.
type bindingHost struct {
	engine *Engine
	op     *Operation
}

var _ analyzeHost = (*bindingHost)(nil)

// analysisHost returns the binding host for op.
func (e *Engine) analysisHost(op *Operation) *bindingHost {
	return &bindingHost{engine: e, op: op}
}

func (h *bindingHost) Authorization() (AuthorizationView, error) {
	return h.engine.authorizationLocked(h.op)
}

func (h *bindingHost) Definition() definitionReader {
	if h.op == nil {
		return nil
	}
	return h.op.definition
}

func (h *bindingHost) AuthorizationStorage(auth AuthorizationView, address ids.ObjectAddress) *storage.Relation {
	return h.engine.authorizationStorageLocked(auth, address)
}

func (h *bindingHost) AdmittedReference(auth AuthorizationView, name parser.ObjectName, ref ids.ObjectRef) (ids.ObjectRef, error) {
	return h.engine.admittedReference(auth, name, ref)
}

func (h *bindingHost) ResolveReference(auth AuthorizationView, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.engine.resolveReference(auth, name)
}

func (h *bindingHost) CatalogOwner() *catalog.Owner { return h.engine.catalog.owner() }

func (h *bindingHost) CurrentCatalog() catalog.Read {
	return h.engine.catalog.read(h.engine.catalog.state)
}

func (h *bindingHost) LookupNamespaces(auth AuthorizationView, name parser.ObjectName, kind lookupKind) (namespaceSelection, error) {
	return h.engine.lookupNamespaces(auth, name, kind)
}

func (h *bindingHost) LookupNamespacesSnapshot(auth AuthorizationView, name parser.ObjectName, kind lookupKind) (namespaceSelection, error) {
	return h.engine.lookupNamespacesSnapshot(auth, name, kind)
}

func (h *bindingHost) LookupPath(auth AuthorizationView, kind lookupKind) ([]ids.ObjectAddress, error) {
	return h.engine.lookupPath(auth, kind)
}

func (h *bindingHost) ReferencePath(auth AuthorizationView) ([]ids.ObjectAddress, error) {
	return h.engine.referencePath(auth)
}

func (h *bindingHost) Regclass(auth AuthorizationView, text string) (ids.ObjectRef, error) {
	return h.engine.regclassLocked(auth, text)
}

func (h *bindingHost) LookupCollation(raw parser.ObjectName) (uint32, error) {
	return h.engine.lookupCollation(raw, h.op)
}

func (h *bindingHost) LookupIndexClass(auth AuthorizationView, raw parser.ObjectName, method catalog.IndexMethod) (sqltype.IndexClass, error) {
	return h.engine.lookupIndexClass(auth, raw, method)
}

func (h *bindingHost) NamedType(auth AuthorizationView, name parser.ObjectName) (*catalog.Type, error) {
	return h.engine.namedType(auth, name)
}

func (h *bindingHost) ResolveType(raw parser.TypeName) (sqltype.Usage, error) {
	return h.engine.resolveType(raw, h.op)
}

func (h *bindingHost) ResolveAnnotationType(raw parser.TypeName, unknownNull bool) (sqltype.Usage, error) {
	return h.engine.resolveAnnotationType(raw, h.op, unknownNull)
}

func (h *bindingHost) ResolveStringAnnotationType(raw parser.TypeName, unknownNull bool) (sqltype.Usage, error) {
	return h.engine.resolveStringAnnotationType(raw, h.op, unknownNull)
}

func (h *bindingHost) ResolveAnnotationTypeInput(raw parser.TypeName, unknownNull, domainBase, stringInput, jsonReturning, thresholdArrayInput bool) (sqltype.Usage, error) {
	return h.engine.resolveAnnotationTypeInput(raw, h.op, unknownNull, domainBase, stringInput, jsonReturning, thresholdArrayInput)
}

func (h *bindingHost) ResolveTableColumnType(raw parser.TypeName) (sqltype.Usage, error) {
	return h.engine.resolveTableColumnType(raw, h.op)
}

func (h *bindingHost) ResolveColumnCollation(u sqltype.Usage, raw *parser.ObjectName) (sqltype.Usage, error) {
	return h.engine.resolveColumnCollation(u, raw, h.op)
}

func (h *bindingHost) CaptureElementInput(auth AuthorizationView, input *sqltype.InputEnvironment) error {
	return h.engine.captureElementInput(auth, h.op, input)
}

func (h *bindingHost) CaptureOperatorInput(auth AuthorizationView) (capturedOperatorInput, error) {
	return h.engine.captureOperatorInput(auth, h.op)
}

func (h *bindingHost) CaptureRegtypeInput(auth AuthorizationView) (capturedRegtypeInput, error) {
	return h.engine.captureRegtypeInput(auth, h.op)
}

func (h *bindingHost) CaptureTypeDiagnosticInput(auth AuthorizationView, input *sqltype.InputEnvironment, typ *sqltype.Descriptor) error {
	return h.engine.captureTypeDiagnosticInput(auth, h.op, input, typ)
}

func (h *bindingHost) CaptureTypeDiagnosticName(auth AuthorizationView) (func(sqltype.ID) string, error) {
	return h.engine.captureTypeDiagnosticName(auth)
}

func (h *bindingHost) CaptureEnumOperationInput(auth AuthorizationView, input *sqltype.InputEnvironment) error {
	return h.engine.captureEnumOperationInput(auth, input)
}

func (h *bindingHost) CaptureEnumOperationSafety(input *sqltype.InputEnvironment) {
	captureEnumOperationSafety(h.op, input)
}

func (h *bindingHost) FTSInput() sqltype.FTSInputNotices { return capturedFTSInput{op: h.op} }

func (h *bindingHost) PlanOwner() (ids.SessionToken, uint64, bool) {
	if h.op == nil {
		return ids.SessionToken{}, 0, false
	}
	return h.op.session.token, h.op.session.discardPlansGeneration, true
}

func (h *bindingHost) EmitWarning(diagnostic sqlstate.Error) {
	if h.op != nil {
		h.op.emitNotice(levelWarning, diagnostic)
	}
}

func (h *bindingHost) EmitNotice(diagnostic sqlstate.Error) {
	if h.op != nil {
		h.op.emitNotice(levelNotice, diagnostic)
	}
}

func (h *bindingHost) ReplayInsertAuthority() bool {
	return h.op != nil && h.op.replayOrZero().insertAuthority
}

func (h *bindingHost) ReplayDomainCell(b *binding, s *parser.Insert, row, column int) (expr.Expr, bool, error) {
	return b.capturedDomainReplayCell(h.op, s, row, column)
}

// DecodeDomainLiteral decodes a domain-constrained literal through the
// statement scope's locked domain input. auth is the binder's authority, or
// nil for the operation's current authority.
func (h *bindingHost) DecodeDomainLiteral(ctx context.Context, auth *AuthorizationView, read catalog.Read, typ *sqltype.Descriptor, text string, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error) {
	owner, ok := input.LockedDomainInput.(*StatementScope)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing locked domain literal input boundary")
	}
	var authority AuthorizationView
	if auth != nil {
		authority = *auth
	} else {
		var err error
		authority, err = h.engine.authorizationLocked(h.op)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if owner.routineAuth.engine != nil && owner.foreignActionAuth == nil {
		authority.State.CurrentUser = owner.routineAuth.State.CurrentUser
		if err := authority.Roles.ValidatePrincipal(authority.State.CurrentUser); err != nil {
			return sqltype.Value{}, err
		}
	} else if owner.routineAuth.engine == nil {
		owner.routineAuth = authority
	}
	eval := EvalContext{evalStatement: evalStatement{Context: ctx, host: hostOf(owner), catalog: read, Input: input, Output: owner.output}, evalAdmission: evalAdmission{routineAuthority: &authority}}
	input.DomainChecks = &eval
	return owner.DecodeLockedDomainInput(ctx, typ, text, typmod, input)
}

// IndexExpressionBinder builds a nested binder over an index's parent
// relation under the operation's current context.
func (h *bindingHost) IndexExpressionBinder(input sqltype.InputEnvironment, auth AuthorizationView, parent *catalog.Relation) *expressionBinder {
	return h.engine.indexExpressionBinder(h.op, input, auth, parent)
}

func (h *bindingHost) BindViewWrite(b *binding, auth AuthorizationView, stmt parser.Statement, consumed ...ids.ObjectRef) error {
	return h.engine.bindViewWrite(b, auth, stmt, consumed...)
}

// bindingSessionToken is the binding session's token, or the zero token without an
// operation.
func bindingSessionToken(host analyzeHost) ids.SessionToken {
	owner, _, _ := host.PlanOwner()
	return owner
}
