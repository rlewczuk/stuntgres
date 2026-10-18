package functions

import (
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
}
