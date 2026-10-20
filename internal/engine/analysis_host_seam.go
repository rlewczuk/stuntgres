package engine

import (
	"context"

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
	// MapBoundQueryExpression is the same walker with identity types and
	// queryMapper applied to subquery roots; it reports whether node changed.
	MapBoundQueryExpression(view catalog.Read, node expr.Expr, queryMapper func(plan.Node) (plan.Node, error), leaf func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, bool, error)

	// Stored views. View definitions stay engine payloads: the binder asks
	// whether a CTE body's stored sources are volatile, and has a stored
	// view's retained expressions capture the binder's input providers.
	StoredViewVolatile(auth analyze.Authority, root plan.Node) (bool, error)
	// ValidateViewReadGraph rejects an active-path recursion among the stored
	// views leaves read, and views that are no longer current.
	ValidateViewReadGraph(view catalog.Read, leaves []exec.Leaf) error
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

// bindingSessionToken is the binding session's token, or the zero token without an
// operation.
func bindingSessionToken(host analyzeHost) ids.SessionToken {
	owner, _, _ := host.PlanOwner()
	return owner
}
