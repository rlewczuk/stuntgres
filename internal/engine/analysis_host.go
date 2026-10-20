package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
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
	// Authority. Binding authorities read the stored authority through the
	// operation's definition reader (analysis_authority.go).
	BindingAuthority(base analyze.Authority) (analyze.Authority, error)
	BindingDefinition(base analyze.Authority) catalog.DefinitionReader
	DefinitionOf(auth analyze.Authority) catalog.DefinitionReader
	CheckPrivilege(auth analyze.Authority, req access.Requirement) error
	CanUse(auth analyze.Authority, ref ids.ObjectRef) error
	StagedTemporary(auth analyze.Authority) ids.ObjectAddress
	Roles(auth analyze.Authority) authorization.Read
	DefinitionStamp(auth analyze.Authority, d catalog.Descriptor) ids.DefinitionRevision
	AuthorizationStorage(auth analyze.Authority, address ids.ObjectAddress) *storage.Relation
	AdmittedReference(auth analyze.Authority, name parser.ObjectName, ref ids.ObjectRef) (ids.ObjectRef, error)
	ResolveReference(auth analyze.Authority, name parser.ObjectName) (ids.ObjectRef, error)

	// Catalog.
	CatalogOwner() *catalog.Owner
	CurrentCatalog() catalog.Read
	RelationHasRules(relation *catalog.Relation) bool

	// Names.
	LookupNamespaces(auth analyze.Authority, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error)
	LookupNamespacesSnapshot(auth analyze.Authority, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error)
	LookupPath(auth analyze.Authority, kind analyze.LookupKind) ([]ids.ObjectAddress, error)
	ReferencePath(auth analyze.Authority) ([]ids.ObjectAddress, error)
	Regclass(auth analyze.Authority, text string) (ids.ObjectRef, error)
	LookupCollation(raw parser.ObjectName) (uint32, error)
	LookupIndexClass(auth analyze.Authority, raw parser.ObjectName, method catalog.IndexMethod) (sqltype.IndexClass, error)
	NamedType(auth analyze.Authority, name parser.ObjectName) (*catalog.Type, error)

	// Types.
	ResolveType(raw parser.TypeName) (sqltype.Usage, error)
	ResolveAnnotationType(raw parser.TypeName, unknownNull bool) (sqltype.Usage, error)
	ResolveStringAnnotationType(raw parser.TypeName, unknownNull bool) (sqltype.Usage, error)
	ResolveAnnotationTypeInput(raw parser.TypeName, unknownNull, domainBase, stringInput, jsonReturning, thresholdArrayInput bool) (sqltype.Usage, error)
	ResolveTableColumnType(raw parser.TypeName) (sqltype.Usage, error)
	ResolveColumnCollation(u sqltype.Usage, raw *parser.ObjectName) (sqltype.Usage, error)

	// Captures.
	CaptureElementInput(auth analyze.Authority, input *sqltype.InputEnvironment) error
	ResolveOperatorText(ctx context.Context, auth analyze.Authority, pos parser.Position, regoper bool, text string) (uint32, []ids.ObjectAddress, []catalog.ExpressionDependency, error)
	ResolveRegtypeText(ctx context.Context, auth analyze.Authority, pos parser.Position, text string) (uint32, []ids.ObjectAddress, error)
	CaptureTypeDiagnosticInput(auth analyze.Authority, input *sqltype.InputEnvironment, typ *sqltype.Descriptor) error
	CaptureTypeDiagnosticName(auth analyze.Authority) (func(sqltype.ID) string, error)
	CaptureEnumOperationInput(auth analyze.Authority, input *sqltype.InputEnvironment) error
	CaptureEnumOperationSafety(input *sqltype.InputEnvironment)
	FTSInput() sqltype.FTSInputNotices

	// Expressions. The engine's copy-on-change retag walker, with identity
	// type and query mappers; leaf runs before children.
	MapBoundExpression(view catalog.Read, reader catalog.DefinitionReader, node expr.Expr, substituteSources bool, leaf func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, error)

	// Stored views. View definitions stay engine payloads: the binder asks
	// whether a CTE body's stored sources are volatile, and has a stored
	// view's retained expressions capture the binder's input providers.
	StoredViewVolatile(auth analyze.Authority, root plan.Node) (bool, error)
	CaptureRetainedViewInput(auth analyze.Authority, relation *catalog.Relation, element func() error, diagnostic func(*sqltype.Descriptor) error) error

	// Session.
	PlanOwner() (owner ids.SessionToken, discardGeneration uint64, ok bool)

	// Effects.
	EmitWarning(diagnostic sqlstate.Error)
	EmitNotice(diagnostic sqlstate.Error)
	ReplayInsertAuthority() bool
	ReplayDomainCell(b *binding, s *parser.Insert, row, column int) (expr.Expr, bool, error)

	// Upward.
	DecodeDomainLiteral(ctx context.Context, auth analyze.Authority, read catalog.Read, typ *sqltype.Descriptor, text string, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error)
	IndexExpressionBinder(input sqltype.InputEnvironment, auth analyze.Authority, parent *catalog.Relation) *expressionBinder
	BindViewWrite(b *binding, auth analyze.Authority, stmt parser.Statement, consumed ...ids.ObjectRef) error
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

// analysisHost returns the binding host for op. A session operation carries
// its own host value and the engine one for binding without an operation
// (bootstrap), so binding allocates no host; copied operations get a fresh
// host.
func (e *Engine) analysisHost(op *Operation) *bindingHost {
	if op != nil && op.analysis.op == op && op.analysis.engine == e {
		return &op.analysis
	}
	if op == nil && e.analysis.engine == e {
		return &e.analysis
	}
	return &bindingHost{engine: e, op: op}
}

// MapBoundExpression rewrites node with leaf through the retag walker,
// keeping every type and query node; it reads only its arguments.
func (h *bindingHost) MapBoundExpression(view catalog.Read, reader catalog.DefinitionReader, node expr.Expr, substituteSources bool, leaf func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, error) {
	next, _, err := mapBoundExpressionReader(view, reader, node, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, func(root plan.Node) (plan.Node, error) { return root, nil }, nil, substituteSources, leaf)
	return next, err
}

func (h *bindingHost) CheckPrivilege(auth analyze.Authority, req access.Requirement) error {
	return h.view(auth).Check(req)
}

func (h *bindingHost) CanUse(auth analyze.Authority, ref ids.ObjectRef) error {
	return h.view(auth).CanUse(ref)
}

func (h *bindingHost) StagedTemporary(auth analyze.Authority) ids.ObjectAddress {
	base, _ := authorityBase(auth)
	return base.stagedTemporary
}

func (h *bindingHost) Roles(auth analyze.Authority) authorization.Read {
	base, _ := authorityBase(auth)
	return base.Roles
}

// DefinitionStamp is the provider stamp of d as auth's session sees it.
func (h *bindingHost) DefinitionStamp(auth analyze.Authority, d catalog.Descriptor) ids.DefinitionRevision {
	base, _ := authorityBase(auth)
	return preparedDefinitionStamp(d, base.session)
}

func (h *bindingHost) AuthorizationStorage(auth analyze.Authority, address ids.ObjectAddress) *storage.Relation {
	return h.engine.authorizationStorageLocked(h.view(auth), address)
}

func (h *bindingHost) AdmittedReference(auth analyze.Authority, name parser.ObjectName, ref ids.ObjectRef) (ids.ObjectRef, error) {
	return h.engine.admittedReference(h.view(auth), name, ref)
}

// RelationHasRules reports whether relation carries rewrite rules, which
// only the engine's rule payload can tell.
func (h *bindingHost) RelationHasRules(relation *catalog.Relation) bool {
	return len(rulesOf(relation)) != 0
}

func (h *bindingHost) ResolveReference(auth analyze.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.engine.resolveReference(h.view(auth), name)
}

func (h *bindingHost) CatalogOwner() *catalog.Owner { return h.engine.catalog.owner() }

func (h *bindingHost) CurrentCatalog() catalog.Read {
	return h.engine.catalog.read(h.engine.catalog.state)
}

func (h *bindingHost) LookupNamespaces(auth analyze.Authority, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error) {
	return h.engine.lookupNamespaces(h.view(auth), name, kind)
}

func (h *bindingHost) LookupNamespacesSnapshot(auth analyze.Authority, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error) {
	return h.engine.lookupNamespacesSnapshot(h.view(auth), name, kind)
}

func (h *bindingHost) LookupPath(auth analyze.Authority, kind analyze.LookupKind) ([]ids.ObjectAddress, error) {
	return h.engine.lookupPath(h.view(auth), kind)
}

func (h *bindingHost) ReferencePath(auth analyze.Authority) ([]ids.ObjectAddress, error) {
	return h.engine.referencePath(h.view(auth))
}

func (h *bindingHost) Regclass(auth analyze.Authority, text string) (ids.ObjectRef, error) {
	return h.engine.regclassLocked(h.view(auth), text)
}

func (h *bindingHost) LookupCollation(raw parser.ObjectName) (uint32, error) {
	return h.engine.lookupCollation(raw, h.op)
}

func (h *bindingHost) LookupIndexClass(auth analyze.Authority, raw parser.ObjectName, method catalog.IndexMethod) (sqltype.IndexClass, error) {
	return h.engine.lookupIndexClass(h.view(auth), raw, method)
}

func (h *bindingHost) NamedType(auth analyze.Authority, name parser.ObjectName) (*catalog.Type, error) {
	return h.engine.namedType(h.view(auth), name)
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

func (h *bindingHost) CaptureElementInput(auth analyze.Authority, input *sqltype.InputEnvironment) error {
	return h.engine.captureElementInput(h.view(auth), h.op, input)
}

// ResolveOperatorText resolves one uncaptured regoper or regoperator literal
// under a fresh capture and returns the namespaces it qualified and the
// providers it resolved. Capture errors come back as they are; resolution
// errors carry the literal's position.
func (h *bindingHost) ResolveOperatorText(ctx context.Context, auth analyze.Authority, pos parser.Position, regoper bool, text string) (uint32, []ids.ObjectAddress, []catalog.ExpressionDependency, error) {
	input, err := h.engine.captureOperatorInput(h.view(auth), h.op)
	if err != nil {
		return 0, nil, nil, err
	}
	var qualified []ids.ObjectAddress
	var operands []catalog.ExpressionDependency
	input.types.position = pos
	input.qualified = &qualified
	input.operands = &operands
	var id uint32
	if regoper {
		id, err = input.ResolveRegoper(ctx, text)
	} else {
		id, err = input.ResolveRegoperator(ctx, text)
	}
	if err != nil {
		return 0, nil, nil, analyze.RegtypeInputError(err, pos)
	}
	return id, qualified, operands, nil
}

// ResolveRegtypeText resolves one uncaptured regtype literal under a fresh
// capture and returns the namespaces it qualified. Capture errors come back as
// they are; resolution errors carry the literal's position.
func (h *bindingHost) ResolveRegtypeText(ctx context.Context, auth analyze.Authority, pos parser.Position, text string) (uint32, []ids.ObjectAddress, error) {
	input, err := h.engine.captureRegtypeInput(h.view(auth), h.op)
	if err != nil {
		return 0, nil, err
	}
	var qualified []ids.ObjectAddress
	input.position = pos
	input.qualified = &qualified
	id, err := input.ResolveRegtype(ctx, text)
	if err != nil {
		return 0, nil, analyze.RegtypeInputError(err, pos)
	}
	return id, qualified, nil
}

func (h *bindingHost) CaptureTypeDiagnosticInput(auth analyze.Authority, input *sqltype.InputEnvironment, typ *sqltype.Descriptor) error {
	return h.engine.captureTypeDiagnosticInput(h.view(auth), h.op, input, typ)
}

func (h *bindingHost) CaptureTypeDiagnosticName(auth analyze.Authority) (func(sqltype.ID) string, error) {
	return h.engine.captureTypeDiagnosticName(h.view(auth))
}

func (h *bindingHost) CaptureEnumOperationInput(auth analyze.Authority, input *sqltype.InputEnvironment) error {
	return h.engine.captureEnumOperationInput(h.view(auth), input)
}

func (h *bindingHost) CaptureEnumOperationSafety(input *sqltype.InputEnvironment) {
	captureEnumOperationSafety(h.op, input)
}

func (h *bindingHost) FTSInput() sqltype.FTSInputNotices { return capturedFTSInput{op: h.op} }

func (h *bindingHost) StoredViewVolatile(auth analyze.Authority, root plan.Node) (bool, error) {
	return cteStoredViewVolatile(auth.ReadCatalog(), root)
}

func (h *bindingHost) CaptureRetainedViewInput(auth analyze.Authority, relation *catalog.Relation, element func() error, diagnostic func(*sqltype.Descriptor) error) error {
	return captureRetainedDefinitionInput(auth.ReadCatalog(), viewOf(relation), element, diagnostic)
}

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
	return capturedDomainReplayCell(b, h.op, s, row, column)
}

// DecodeDomainLiteral decodes a domain-constrained literal through the
// statement scope's locked domain input. auth is the binder's stored
// authority, or nil for the operation's current authority.
func (h *bindingHost) DecodeDomainLiteral(ctx context.Context, auth analyze.Authority, read catalog.Read, typ *sqltype.Descriptor, text string, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error) {
	owner, ok := input.LockedDomainInput.(*StatementScope)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing locked domain literal input boundary")
	}
	var authority AuthorizationView
	if auth != nil {
		authority = h.view(auth)
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
	eval := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(owner), Catalog: read, Input: input, Output: owner.output}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &authority}}
	input.DomainChecks = &eval
	return owner.DecodeLockedDomainInput(ctx, typ, text, typmod, input)
}

// IndexExpressionBinder builds a nested binder over an index's parent
// relation under the operation's current context.
func (h *bindingHost) IndexExpressionBinder(input sqltype.InputEnvironment, auth analyze.Authority, parent *catalog.Relation) *expressionBinder {
	return h.engine.indexExpressionBinder(h.op, input, h.view(auth), parent)
}

func (h *bindingHost) BindViewWrite(b *binding, auth analyze.Authority, stmt parser.Statement, consumed ...ids.ObjectRef) error {
	return h.engine.bindViewWrite(b, h.view(auth), stmt, consumed...)
}

// bindingSessionToken is the binding session's token, or the zero token without an
// operation.
func bindingSessionToken(host analyzeHost) ids.SessionToken {
	owner, _, _ := host.PlanOwner()
	return owner
}

// newExpressionBinder is the root construction point: it binds through the
// engine's host for op.
func newExpressionBinder(ctx context.Context, e *Engine, input sqltype.InputEnvironment, op *Operation, expected *StatementDescription) *expressionBinder {
	return newHostedBinder(ctx, e.analysisHost(op), input, expectedOf(expected))
}
