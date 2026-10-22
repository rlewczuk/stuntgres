package functions

import (
	"context"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Binder is what builtin bind code may ask of the engine's analyzer. The
// engine's expression binder implements it. Every method is a reviewed
// decision recorded in this package's README; prefer passing values to adding
// methods.
type Binder interface {
	ArgumentAnalyzer
	ArgumentCoercer

	// Type returns the binding catalog's descriptor for a builtin type.
	Type(sqltype.ID) *sqltype.Descriptor
	// Info builds node metadata and records the usage's type and collation
	// as dependencies of the expression being bound.
	Info(sqltype.Usage, parser.Position, expr.Properties) expr.Info
	// BuiltinVisible reports whether function lookup for the name reaches
	// pg_catalog under the current search_path. Lookup errors propagate.
	BuiltinVisible(parser.ObjectName) (bool, error)
	// BuiltinRoutineInfo attaches the selected builtin routine identity to
	// info. The provider must be associated with the declaration.
	BuiltinRoutineInfo(expr.Info, RoutineProvider, string, ...sqltype.ID) (expr.Info, error)
	// BuiltinRoutineReference captures the selected builtin routine and
	// returns its reference without building node metadata.
	BuiltinRoutineReference(RoutineProvider, string, ...sqltype.ID) (ids.ObjectRef, error)
	// DiagnosticTypeName spells a type as PostgreSQL diagnostics do,
	// including the qualified names of user-defined types.
	DiagnosticTypeName(*sqltype.Descriptor) string
	// SignatureError returns the 42883 "function ... does not exist" error
	// for the analyzed argument type names. The engine also uses it to decide
	// whether a one-argument call may fall back to composite field selection.
	SignatureError(parser.ObjectName, parser.Position, []string) error
	// RecordUnresolvedCallable records the identity of an explicitly missing
	// callable diagnostic. It neither notes composite-field recovery nor
	// changes the error; only a terminal propagated identity permits deferral.
	RecordUnresolvedCallable(error)
	// AnalyzeFilter analyzes a FILTER clause as a boolean in the FILTER
	// context and discards it. Scalar functions use it to report FILTER
	// errors in PostgreSQL's order before rejecting the decoration.
	AnalyzeFilter(parser.Expr) error
	// DefaultConsumer reports whether a column DEFAULT expression is being
	// bound. Families outside the admitted DEFAULT set reject themselves.
	DefaultConsumer() bool
	// ArrayType returns the array type of an element type as the binding
	// catalog (or the statement's definition stage) declares it.
	ArrayType(element *sqltype.Descriptor) (*sqltype.Descriptor, bool)
	// CommonUsage is PostgreSQL's nonthrowing common-type probe over typed
	// operand usages. It returns the zero Usage on a category mismatch and
	// TEXT when no usage is given.
	CommonUsage([]sqltype.Usage) sqltype.Usage
	// ComparisonInfo derives the comparison input collation of two bound
	// operands and records their diagnostic type inputs.
	ComparisonInfo(left, right expr.Expr) (expr.Info, error)
	// CollatedInfo derives info's result collation from the bound inputs,
	// reporting collation conflicts as the engine's collation rules do.
	CollatedInfo(info expr.Info, inputs ...expr.Expr) (expr.Info, error)
	// RetainUntypedParameter keeps an argument bound to an ANY formal pending
	// when it is a parameter whose type is still unresolved, so statement
	// completion reports 42P08 unless another occurrence resolves it. It
	// reports whether the argument was such a parameter.
	RetainUntypedParameter(expr.Expr) bool
	// NoteCatalogText records that the statement renders a catalog-identifier
	// value as text, so execution captures the catalog snapshot it reads.
	NoteCatalogText()
	// NoteCatalogSnapshot records analysis-only catalog demand, separately from
	// NoteCatalogText's execution binding flag.
	NoteCatalogSnapshot()
	// InstalledRoutine resolves name on the function search path, propagating
	// lookup errors, and returns the routine that extension installed there
	// with the signature (comma-separated input type OIDs), or nil.
	InstalledRoutine(name parser.ObjectName, extension Extension, signature string) (InstalledRoutine, error)
	// BuiltinOperatorRoutineInfo attaches the selected builtin operator's
	// routine identity to info, as BuiltinRoutineInfo does for functions.
	BuiltinOperatorRoutineInfo(info expr.Info, operator string, left, right sqltype.ID) (expr.Info, error)
	// CaptureComparisonInput records the statement's diagnostic type-name
	// input when comparing values of the type needs it.
	CaptureComparisonInput(*sqltype.Descriptor) error
	// Literal returns the engine's literal node for a constant, positioned at
	// pos with immutable properties; omitted default arguments use it.
	Literal(sqltype.Value, parser.Position) expr.Expr
	// Context is the binding statement's cancellation context, for input
	// conversions performed while binding.
	Context() context.Context
	// AdmitSetReturning rejects a set-returning call where the current clause
	// or consumer forbids one, and otherwise records a target-list set.
	AdmitSetReturning(parser.Position) error
	// CaptureTypeInput captures the diagnostic-name, ACL and catalog
	// identifier input that values of the type need.
	CaptureTypeInput(*sqltype.Descriptor) error
	// ConstantValue returns the value of a literal node built by the
	// analyzer; ok is false for every other node.
	ConstantValue(expr.Expr) (sqltype.Value, bool)
	// BuiltinOperatorVisible reports whether operator lookup for the name
	// reaches pg_catalog under the current search_path.
	BuiltinOperatorVisible(parser.ObjectName) (bool, error)
	// CallSignatureError is the analyzer's whole-call 42883: it re-analyzes
	// the arguments for their diagnostic names, reports composite-candidate
	// calls as unsupported, and notes the missing signature.
	CallSignatureError(parser.FunctionExpr) error
	// LiteralNull reports whether a bound argument is a NULL literal, looking
	// through collation and coercion wrappers.
	LiteralNull(expr.Expr) bool
	// NoteVisibilitySnapshot records that execution reads catalog visibility
	// (encoding conversion lookup), so the statement captures that snapshot.
	NoteVisibilitySnapshot()
	// Frame identifies the query frame being bound; the variadic string
	// family's quote-capture folding keys on it.
	Frame() uint32
	// CapturePolymorphicBody captures the builtin routines a SQL-bodied
	// builtin selects from the node's actual argument.
	CapturePolymorphicBody(expr.Expr) error
	// TargetSet records a set-returning call bound directly in a clause that
	// projects rows (SELECT, GROUP BY, ORDER BY, DISTINCT ON, windows),
	// without AdmitSetReturning's placement rules.
	TargetSet()
	// RegexpSetPlacement rejects a set-returning regexp call where the
	// current clause or consumer does not admit one (the regexp family's own
	// placement policy).
	RegexpSetPlacement(RegexpCall) error
	// JSONReturningType resolves a SQL/JSON RETURNING type name as the
	// statement's type input does, rejecting the RETURNING-forbidden forms.
	JSONReturningType(parser.TypeName) (sqltype.Usage, error)
	// SequenceArgument binds a sequence call's regclass provider input. It
	// owns prepared sequence-literal capture (held constants keep their
	// captured identity) and, in a column DEFAULT, requires a non-NULL
	// captured literal. ref is zero unless the input is such a literal.
	SequenceArgument(parser.Expr) (node expr.Expr, ref ids.ObjectRef, err error)
	// SequenceTemporary returns the TEMP provenance captured with ref by
	// SequenceArgument. In a column DEFAULT it validates that provider for
	// the consumer and ends call-site literal capture.
	SequenceTemporary(ids.ObjectRef, parser.Position) (ids.SessionToken, error)
}

// AggregateBinder is the analyzer as aggregate bind code sees it: the scalar
// Binder plus the capabilities that are valid only while an aggregate call
// (parser.AggregateExpr) is bound, where the engine owns grouping state.
type AggregateBinder interface {
	Binder
	// AnalyzeAggregateFilter analyzes an aggregate's FILTER clause as a
	// boolean in the FILTER context and returns it, or nil without FILTER.
	// Aggregates analyze it after their arguments, before resolution.
	AnalyzeAggregateFilter(parser.AggregateExpr) (expr.Expr, error)
	// FinishAggregate completes an analyzed aggregate call with the engine's
	// grouping state: it binds ORDER BY and DISTINCT, checks window nesting,
	// registers (or merges) the call and returns the occurrence node.
	FinishAggregate(raw parser.AggregateExpr, call AggregateCall, filter expr.Expr, info expr.Info) (expr.Expr, error)
	// DomainTypeName spells an aggregate input type as min, max, range_agg
	// and the ordered-set family report it: a domain is named through the
	// type search path, propagating lookup errors; other types use their
	// diagnostic name, and nil is "unknown".
	DomainTypeName(*sqltype.Descriptor) (string, error)
	// CollationName spells a collation for diagnostics through the binding
	// catalog.
	CollationName(uint32) string
	// CommonArgumentUsage is PostgreSQL's labeled common-type selection over
	// analyzed operands (select_common_type), reporting the label's 42804.
	CommonArgumentUsage(operands []Argument, label string) (sqltype.Usage, error)
	// CollationRelabel wraps child in the engine's metadata-only node that
	// presents info's usage and collation without converting values.
	CollationRelabel(child expr.Expr, info expr.Info) expr.Expr
}
