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
	// completion reports 42P08 unless another occurrence resolves it.
	RetainUntypedParameter(expr.Expr)
	// NoteCatalogText records that the statement renders a catalog-identifier
	// value as text, so execution captures the catalog snapshot it reads.
	NoteCatalogText()
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
	// FinishBinary finishes a selected bytea/bit call. It is a leak: string
	// overloads route to the binary family, which the engine still binds.
	// The binary port removes it.
	FinishBinary(name string, family sqltype.ID, pos parser.Position, operands []Argument, syntax parser.FunctionSyntax, types *BinaryTypes) (expr.Expr, error)
}
