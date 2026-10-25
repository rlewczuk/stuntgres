package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// BindingHost is the root adapter behind analyze.Host. Binders also run
// without a StatementScope (DDL, defaults, checks, index expressions,
// generated columns, Restore), so it holds the engine and the operation
// rather than a scope. A nil op means no definition reader, no notices, no
// plan owner and no Restore replay, as before the seam.
type BindingHost struct {
	Engine *Engine
	Op     *session.Operation
}

var _ analyze.Host = (*BindingHost)(nil)

func (*BindingHost) AnalysisSlot() {}

// AnalysisHost returns the binding host for op. A session operation carries
// its own host, allocated with it (sessionHost.NewOperation), and the engine one for
// binding without an operation (bootstrap), so binding allocates no host;
// copied operations get a fresh host.
func (e *Engine) AnalysisHost(op *session.Operation) *BindingHost {
	if op != nil {
		if h, _ := op.Analysis.(*BindingHost); h != nil && h.Op == op && h.Engine == e {
			return h
		}
	}
	if op == nil && e.Analysis.Engine == e {
		return &e.Analysis
	}
	return &BindingHost{Engine: e, Op: op}
}

// MapBoundExpression rewrites node with leaf through the retag walker,
// keeping every type and query node; it reads only its arguments.
func (h *BindingHost) MapBoundExpression(view catalog.Read, reader catalog.DefinitionReader, node expr.Expr, substituteSources bool, leaf func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, error) {
	next, _, err := stored.MapExpressionReader(view, reader, node, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, func(root plan.Node) (plan.Node, error) { return root, nil }, nil, substituteSources, leaf)
	return next, err
}

// MapBoundQueryExpression rewrites node through the retag walker with
// identity types, mapping subquery roots with queryMapper.
func (h *BindingHost) MapBoundQueryExpression(view catalog.Read, node expr.Expr, queryMapper func(plan.Node) (plan.Node, error), leaf func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, bool, error) {
	return stored.MapExpression(view, node, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, queryMapper, nil, leaf)
}

// ValidateViewReadGraph checks the stored views leaves read against view.
func (h *BindingHost) ValidateViewReadGraph(view catalog.Read, leaves []exec.Leaf) error {
	_, err := stored.ValidateViewReadGraph(view, leaves)
	return err
}

func (h *BindingHost) CheckPrivilege(auth analyze.Authority, req access.Requirement) error {
	return h.view(auth).Check(req)
}

func (h *BindingHost) CanUse(auth analyze.Authority, ref ids.ObjectRef) error {
	return h.view(auth).CanUse(ref)
}

func (h *BindingHost) StagedTemporary(auth analyze.Authority) ids.ObjectAddress {
	base, _ := authorityBase(auth)
	return base.StagedTemporary
}

func (h *BindingHost) Roles(auth analyze.Authority) authorization.Read {
	base, _ := authorityBase(auth)
	return base.Roles
}

// DefinitionStamp is the provider stamp of d as auth's session sees it.
func (h *BindingHost) DefinitionStamp(auth analyze.Authority, d catalog.Descriptor) ids.DefinitionRevision {
	base, _ := authorityBase(auth)
	return PreparedDefinitionStamp(d, base.Session)
}

func (h *BindingHost) AuthorizationStorage(auth analyze.Authority, address ids.ObjectAddress) *storage.Relation {
	return h.view(auth).StorageLocked(address)
}

func (h *BindingHost) AdmittedReference(auth analyze.Authority, name parser.ObjectName, ref ids.ObjectRef) (ids.ObjectRef, error) {
	return h.Engine.admittedReference(h.view(auth), name, ref)
}

func (h *BindingHost) ResolveReference(auth analyze.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.Engine.ResolveReference(h.view(auth), name)
}

func (h *BindingHost) CatalogOwner() *catalog.Owner { return &h.Engine.Catalog.Owner }

func (h *BindingHost) CurrentCatalog() catalog.Read {
	return h.Engine.Catalog.Current()
}

func (h *BindingHost) LookupNamespaces(auth analyze.Authority, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error) {
	return h.Engine.LookupNamespaces(h.view(auth), name, kind)
}

func (h *BindingHost) LookupNamespacesSnapshot(auth analyze.Authority, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error) {
	return LookupNamespacesSnapshot(h.view(auth), name, kind)
}

func (h *BindingHost) LookupPath(auth analyze.Authority, kind analyze.LookupKind) ([]ids.ObjectAddress, error) {
	return LookupPath(h.view(auth), kind)
}

func (h *BindingHost) ReferencePath(auth analyze.Authority) ([]ids.ObjectAddress, error) {
	return ReferencePath(h.view(auth))
}

func (h *BindingHost) Regclass(auth analyze.Authority, text string) (ids.ObjectRef, error) {
	return h.Engine.regclassLocked(h.view(auth), text)
}

func (h *BindingHost) LookupCollation(raw parser.ObjectName) (uint32, error) {
	return h.Engine.lookupCollation(raw, h.Op)
}

func (h *BindingHost) LookupIndexClass(auth analyze.Authority, raw parser.ObjectName, method catalog.IndexMethod) (sqltype.IndexClass, error) {
	return h.Engine.lookupIndexClass(h.view(auth), raw, method)
}

func (h *BindingHost) NamedType(auth analyze.Authority, name parser.ObjectName) (*catalog.Type, error) {
	return h.Engine.namedType(h.view(auth), name)
}

func (h *BindingHost) ResolveType(raw parser.TypeName) (sqltype.Usage, error) {
	return h.Engine.ResolveType(raw, h.Op)
}

func (h *BindingHost) ResolveAnnotationType(raw parser.TypeName, unknownNull bool) (sqltype.Usage, error) {
	return h.Engine.resolveAnnotationType(raw, h.Op, unknownNull)
}

func (h *BindingHost) ResolveStringAnnotationType(raw parser.TypeName, unknownNull bool) (sqltype.Usage, error) {
	return h.Engine.resolveStringAnnotationType(raw, h.Op, unknownNull)
}

func (h *BindingHost) ResolveAnnotationTypeInput(raw parser.TypeName, unknownNull, domainBase, stringInput, jsonReturning, thresholdArrayInput bool) (sqltype.Usage, error) {
	return h.Engine.resolveAnnotationTypeInput(raw, h.Op, unknownNull, domainBase, stringInput, jsonReturning, thresholdArrayInput)
}

func (h *BindingHost) ResolveTableColumnType(raw parser.TypeName) (sqltype.Usage, error) {
	return h.Engine.resolveTableColumnType(raw, h.Op)
}

func (h *BindingHost) ResolveColumnCollation(u sqltype.Usage, raw *parser.ObjectName) (sqltype.Usage, error) {
	return h.Engine.ResolveColumnCollation(u, raw, h.Op)
}

func (h *BindingHost) CaptureElementInput(auth analyze.Authority, input *sqltype.InputEnvironment) error {
	return h.Engine.CaptureElementInput(h.view(auth), h.Op, input)
}

func (h *BindingHost) CaptureRegTSInput(auth analyze.Authority, input *sqltype.InputEnvironment) error {
	if input.RegTS != nil {
		return nil
	}
	resolver, err := CaptureRegTSInput(h.view(auth))
	if err == nil {
		input.RegTS = resolver
	}
	return err
}

// ResolveOperatorText resolves one uncaptured regoper or regoperator literal
// under a fresh capture and returns the namespaces it qualified and the
// providers it resolved. Capture errors come back as they are; resolution
// errors carry the literal's position.
func (h *BindingHost) ResolveOperatorText(ctx context.Context, auth analyze.Authority, pos parser.Position, regoper bool, text string) (uint32, []ids.ObjectAddress, []catalog.ExpressionDependency, error) {
	input, err := captureOperatorInput(h.view(auth), h.Op)
	if err != nil {
		return 0, nil, nil, err
	}
	var qualified []ids.ObjectAddress
	var operands []catalog.ExpressionDependency
	resolver := input.RetargetOperator(analyze.IdentifierTarget{Position: pos, Qualified: &qualified, Operands: &operands})
	var id uint32
	if regoper {
		id, err = resolver.ResolveRegoper(ctx, text)
	} else {
		id, err = resolver.ResolveRegoperator(ctx, text)
	}
	if err != nil {
		return 0, nil, nil, analyze.RegtypeInputError(err, pos)
	}
	return id, qualified, operands, nil
}

// ResolveRegtypeText resolves one uncaptured regtype literal under a fresh
// capture and returns the namespaces it qualified. Capture errors come back as
// they are; resolution errors carry the literal's position.
func (h *BindingHost) ResolveRegtypeText(ctx context.Context, auth analyze.Authority, pos parser.Position, text string) (uint32, []ids.ObjectAddress, error) {
	input, err := CaptureRegtypeInput(h.view(auth), h.Op)
	if err != nil {
		return 0, nil, err
	}
	var qualified []ids.ObjectAddress
	id, err := input.RetargetRegtype(analyze.IdentifierTarget{Position: pos, Qualified: &qualified}).ResolveRegtype(ctx, text)
	if err != nil {
		return 0, nil, analyze.RegtypeInputError(err, pos)
	}
	return id, qualified, nil
}

func (h *BindingHost) CaptureTypeDiagnosticInput(auth analyze.Authority, input *sqltype.InputEnvironment, typ *sqltype.Descriptor) error {
	return h.Engine.CaptureTypeDiagnosticInput(h.view(auth), h.Op, input, typ)
}

func (h *BindingHost) CaptureTypeDiagnosticName(auth analyze.Authority) (func(sqltype.ID) string, error) {
	return CaptureTypeDiagnosticName(h.view(auth))
}

func (h *BindingHost) CaptureEnumOperationInput(auth analyze.Authority, input *sqltype.InputEnvironment) error {
	return captureEnumOperationInput(h.view(auth), input)
}

func (h *BindingHost) CaptureEnumOperationSafety(input *sqltype.InputEnvironment) {
	CaptureEnumOperationSafety(h.Op, input)
}

func (h *BindingHost) FTSInput() sqltype.FTSInputNotices { return session.FTSInput{Op: h.Op} }

func (h *BindingHost) StoredViewVolatile(auth analyze.Authority, root plan.Node) (bool, error) {
	return stored.ViewVolatile(auth.ReadCatalog(), root)
}

func (h *BindingHost) CaptureRetainedViewInput(auth analyze.Authority, relation *catalog.Relation, element, parserInput func() error, diagnostic func(*sqltype.Descriptor) error) error {
	return stored.CaptureRetainedInput(auth.ReadCatalog(), stored.ViewOf(relation), element, parserInput, diagnostic)
}

func (h *BindingHost) PlanOwner() (ids.SessionToken, uint64, bool) {
	if h.Op == nil {
		return ids.SessionToken{}, 0, false
	}
	return h.Op.Session.SessionToken, h.Op.Session.DiscardPlansGeneration, true
}

func (h *BindingHost) EmitWarning(diagnostic sqlstate.Error) {
	if h.Op != nil {
		h.Op.EmitNotice(session.LevelWarning, diagnostic)
	}
}

func (h *BindingHost) EmitNotice(diagnostic sqlstate.Error) {
	if h.Op != nil {
		h.Op.EmitNotice(session.LevelNotice, diagnostic)
	}
}

func (h *BindingHost) ReplayInsertAuthority() bool {
	return h.Op != nil && OperationReplayOrZero(h.Op).InsertAuthority
}

func (h *BindingHost) ReplayConstructorCall(raw parser.FunctionExpr) bool {
	return h.ReplayInsertAuthority() && !dump.RestoreFunctionRecipe(raw) && catalog.RestoreRangeConstructorEligible(h.CurrentCatalog(), raw)
}

func (h *BindingHost) ReplayDomainCell(b *analyze.Binding, s *parser.Insert, row, column int) (expr.Expr, bool, error) {
	return capturedDomainReplayCell(b, h.Op, s, row, column)
}

// DecodeDomainLiteral decodes a domain-constrained literal through the
// statement scope's locked domain input. auth is the binder's stored
// authority, or nil for the operation's current authority.
func (h *BindingHost) DecodeDomainLiteral(ctx context.Context, auth analyze.Authority, read catalog.Read, typ *sqltype.Descriptor, text string, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error) {
	owner, ok := input.LockedDomainInput.(*StatementScope)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing locked domain literal input boundary")
	}
	var authority AuthorizationView
	if auth != nil {
		authority = h.view(auth)
	} else {
		var err error
		authority, err = h.Engine.AuthorizationLocked(h.Op)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if owner.RoutineAuth.live() && owner.ForeignActionAuth == nil {
		authority.State.CurrentUser = owner.RoutineAuth.State.CurrentUser
		if err := authority.Roles.ValidatePrincipal(authority.State.CurrentUser); err != nil {
			return sqltype.Value{}, err
		}
	} else if !owner.RoutineAuth.live() {
		owner.RoutineAuth = authority
	}
	eval := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: HostOf(owner), Catalog: read, Input: input, Output: owner.Output}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &authority}}
	input.DomainChecks = &eval
	return owner.DecodeLockedDomainInput(ctx, typ, text, typmod, input)
}

// IndexExpressionBinder builds a nested binder over an index's parent
// relation under the operation's current context.
func (h *BindingHost) IndexExpressionBinder(input sqltype.InputEnvironment, auth analyze.Authority, parent *catalog.Relation) *analyze.Binder {
	return h.Engine.IndexExpressionBinder(h.Op, input, h.view(auth), parent)
}

func (h *BindingHost) BindViewWrite(b *analyze.Binding, auth analyze.Authority, stmt parser.Statement, consumed ...ids.ObjectRef) error {
	return view.BindViewWrite(&h.Engine.Catalog.Owner, h.view(auth).Catalog, b, stmt, consumed...)
}

// NewExpressionBinder is the root construction point: it binds through the
// engine's host for op.
func NewExpressionBinder(ctx context.Context, e *Engine, input sqltype.InputEnvironment, op *session.Operation, expected *StatementDescription) *analyze.Binder {
	return analyze.NewBinder(ctx, e.AnalysisHost(op), input, expectedOf(expected))
}
