// Package expr defines the expression contract shared by the engine and the
// builtin functions package: bound expressions, their metadata and
// properties, and the evaluation runtime they may use. Concrete engine nodes,
// the plan IR and the engine's evaluation state stay in the engine.
package expr

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Expr is a bound expression. Dependence and volatility propagate
// independently through bound expressions.
type Expr interface {
	ResultUsage() sqltype.Usage
	CollationIndeterminate() bool
	ImplicitCollationConflict() (sqltype.CollationRef, sqltype.CollationRef, parser.Position)
	// ExplicitCollation returns the annotation origin; zero means implicit.
	ExplicitCollation() parser.Position
	SourcePosition() parser.Position
	Properties() Properties
	// Eval evaluates the expression. The context must not be nil.
	Eval(Context) (sqltype.Value, error)
}
