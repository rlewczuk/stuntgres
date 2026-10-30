package analyze

import (
	"context"

	"stuntgres/internal/engine/access"
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

// Host is what statement binding may ask of the engine: the binder
// reaches engine, session and operation state only through it. Each method
// is a reviewed capability. Binding runs with
// Engine.mu held; every method is called with Engine.mu held and never takes
// or releases it.
type Host interface {
	// Authority. Binding authorities read the stored authority through the
	// operation's definition reader (analysis_authority.go).
	BindingAuthority(base Authority) (Authority, error)
	BindingDefinition(base Authority) catalog.DefinitionReader
	DefinitionOf(auth Authority) catalog.DefinitionReader
	CheckPrivilege(auth Authority, req access.Requirement) error
	CanUse(auth Authority, ref ids.ObjectRef) error
	StagedTemporary(auth Authority) ids.ObjectAddress
	Roles(auth Authority) authorization.Read
	DefinitionStamp(auth Authority, d catalog.Descriptor) ids.DefinitionRevision
	AuthorizationStorage(auth Authority, address ids.ObjectAddress) *storage.Relation
	AdmittedReference(auth Authority, name parser.ObjectName, ref ids.ObjectRef) (ids.ObjectRef, error)
	ResolveReference(auth Authority, name parser.ObjectName) (ids.ObjectRef, error)

	// Catalog.
	CatalogOwner() *catalog.Owner
	CurrentCatalog() catalog.Read

	// Names.
	LookupNamespaces(auth Authority, name parser.ObjectName, kind LookupKind) (NamespaceSelection, error)
	LookupNamespacesSnapshot(auth Authority, name parser.ObjectName, kind LookupKind) (NamespaceSelection, error)
	LookupPath(auth Authority, kind LookupKind) ([]ids.ObjectAddress, error)
	ReferencePath(auth Authority) ([]ids.ObjectAddress, error)
	Regclass(auth Authority, text string) (ids.ObjectRef, error)
	LookupCollation(raw parser.ObjectName) (uint32, error)
	LookupIndexClass(auth Authority, raw parser.ObjectName, method catalog.IndexMethod) (sqltype.IndexClass, error)
	NamedType(auth Authority, name parser.ObjectName) (*catalog.Type, error)

	// Types.
	ResolveType(raw parser.TypeName) (sqltype.Usage, error)
	ResolveAnnotationType(raw parser.TypeName, unknownNull bool) (sqltype.Usage, error)
	ResolveAnnotationTypeInput(raw parser.TypeName, unknownNull, domainBase, jsonReturning, thresholdArrayInput bool) (sqltype.Usage, error)
	ResolveColumnCollation(u sqltype.Usage, raw *parser.ObjectName) (sqltype.Usage, error)

	// Captures.
	CaptureElementInput(auth Authority, input *sqltype.InputEnvironment) error
	CaptureRegTSInput(auth Authority, input *sqltype.InputEnvironment) error
	ResolveOperatorText(ctx context.Context, auth Authority, pos parser.Position, regoper bool, text string) (uint32, []ids.ObjectAddress, []catalog.ExpressionDependency, error)
	ResolveRegtypeText(ctx context.Context, auth Authority, pos parser.Position, text string) (uint32, []ids.ObjectAddress, error)
	CaptureTypeDiagnosticInput(auth Authority, input *sqltype.InputEnvironment, typ *sqltype.Descriptor) error
	CaptureTypeDiagnosticName(auth Authority) (func(sqltype.ID) string, error)
	CaptureEnumOperationInput(auth Authority, input *sqltype.InputEnvironment) error
	CaptureEnumOperationSafety(input *sqltype.InputEnvironment)
	FTSInput() sqltype.FTSInputNotices

	// Expressions. The engine's copy-on-change retag walker, with identity
	// type and query mappers; leaf runs before children.
	MapBoundExpression(view catalog.Read, reader catalog.DefinitionReader, node expr.Expr, substituteSources bool, leaf func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, error)
	// MapBoundQueryExpression is the same walker with identity types and
	// queryMapper applied to subquery roots; it reports whether node changed.
	MapBoundQueryExpression(view catalog.Read, node expr.Expr, queryMapper func(plan.Node) (plan.Node, error), leaf func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, bool, error)

	// Stored views. View definitions stay engine payloads: the binder asks
	// whether a CTE body's stored sources are volatile, and has a stored
	// view's retained expressions capture the binder's input providers.
	StoredViewVolatile(auth Authority, root plan.Node) (bool, error)
	// ValidateViewReadGraph rejects an active-path recursion among the stored
	// views leaves read, and views that are no longer current.
	ValidateViewReadGraph(auth Authority, leaves []exec.Leaf) error
	CaptureRetainedViewInput(auth Authority, relation *catalog.Relation, element, parserInput func() error, diagnostic func(*sqltype.Descriptor) error) error

	// Session.
	PlanOwner() (owner ids.SessionToken, discardGeneration uint64, ok bool)

	// Effects.
	EmitWarning(diagnostic sqlstate.Error)
	EmitNotice(diagnostic sqlstate.Error)
	ReplayInsertAuthority() bool
	ReplayConstructorCall(parser.FunctionExpr) bool
	ReplayDomainCell(b *Binding, s *parser.Insert, row, column int) (expr.Expr, bool, error)

	// Upward.
	DecodeDomainLiteral(ctx context.Context, auth Authority, read catalog.Read, typ *sqltype.Descriptor, text string, typmod int32, input sqltype.InputEnvironment) (sqltype.Value, error)
	IndexExpressionBinder(input sqltype.InputEnvironment, auth Authority, parent *catalog.Relation) *Binder
	BindViewWrite(b *Binding, auth Authority, stmt parser.Statement, consumed ...ids.ObjectRef) error
}

// bindingSessionToken is the binding session's token, or the zero token without an
// operation.
func bindingSessionToken(host Host) ids.SessionToken {
	owner, _, _ := host.PlanOwner()
	return owner
}
