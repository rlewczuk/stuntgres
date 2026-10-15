package functions

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type ArgumentKind uint8

const (
	BoundArgument ArgumentKind = iota
	DeferredStringArgument
	DeferredNullArgument
	UnresolvedParameterArgument
)

// A deferred literal has no Expression. An unresolved parameter already has its
// occurrence expression; only the engine can finalize that occurrence's type.
type Argument[E any] struct {
	Expression E
	Raw        parser.Expr
	Usage      sqltype.Usage
	Kind       ArgumentKind
}
type AnalysisIntent uint8

const (
	ComparisonArgument AnalysisIntent = iota
	DirectDeferredArgument
	StringTextArgument
	StringOtherArgument
	StringProjectionArgument
	ProjectionArgument
)

type AnalysisRequest struct{ Intent AnalysisIntent }
type ArgumentAnalyzer[E any] interface {
	AnalyzeArgument(parser.Expr, AnalysisRequest) (Argument[E], error)
}

func ResolveAnyValue(visible, star bool, count int, usage sqltype.Usage) Resolution {
	if !visible || star || count != 1 {
		return Missing
	}
	if !usage.Type.Concrete() {
		return UnknownPolymorphic
	}
	return Resolved
}

// Value windows use anyelement, with an implicit INT4 ordinal only for nth.
// Signature selection precedes polymorphic resolution and value evaluation.
func ResolveWindowValue(visible, star, nth bool, count int, value, ordinal, integer sqltype.Usage) (Resolution, sqltype.CoercionPlan) {
	var plan sqltype.CoercionPlan
	arity := 1
	if nth {
		arity = 2
	}
	if !visible || star || count != arity {
		return Missing, plan
	}
	if nth && ordinal.Type != nil {
		var err error
		plan, err = sqltype.PlanCoercion(ordinal, integer, sqltype.Implicit)
		if err != nil {
			return Missing, plan
		}
	}
	if !value.Type.Concrete() {
		return UnknownPolymorphic, plan
	}
	return Resolved, plan
}

// Ntile has one monomorphic INT4 formal, including unknown literals/parameters.
func ResolveNtile(visible, star bool, count int, argument, integer sqltype.Usage) (Resolution, sqltype.CoercionPlan) {
	var plan sqltype.CoercionPlan
	if !visible || star || count != 1 {
		return Missing, plan
	}
	if argument.Type != nil {
		var err error
		plan, err = sqltype.PlanCoercion(argument, integer, sqltype.Implicit)
		if err != nil {
			return Missing, plan
		}
	}
	return Resolved, plan
}

type CoercionIntent uint8

const (
	FinalizeArgument CoercionIntent = iota
	LikeArgument
	DirectFormalArgument
	ExactFormalArgument
	ConditionalArgument
	TextFormalArgument
	CollatedTextFormalArgument
	SizeFormalArgument
)

type CoercionRequest struct {
	Intent             CoercionIntent
	Target             sqltype.Usage
	Plan               sqltype.CoercionPlan
	Planned            bool
	PreserveProperties bool
	AtRaw              bool
	Label              string
}
type ArgumentCoercer[E any] interface {
	CoerceArgument(Argument[E], CoercionRequest) (E, error)
}

type FunctionLabelIssue struct {
	Malformed  bool
	Positional bool
	Duplicate  string
	Position   parser.Position
}

func FunctionLabels(raw parser.FunctionExpr) FunctionLabelIssue {
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return FunctionLabelIssue{Malformed: true}
	}
	named := false
	for i, label := range raw.ArgNames {
		if label.Text == "" {
			if named {
				return FunctionLabelIssue{Positional: true, Position: raw.Args[i].SourcePosition()}
			}
			continue
		}
		named = true
		for _, previous := range raw.ArgNames[:i] {
			if previous.Text == label.Text {
				return FunctionLabelIssue{Duplicate: label.Text, Position: label.Pos}
			}
		}
	}
	return FunctionLabelIssue{}
}

func AnalyzeDirectArguments[E any, B ArgumentAnalyzer[E]](b B, raw []parser.Expr) ([]E, []bool, error) {
	var args []E
	var unknown []bool
	if len(raw) <= 100 {
		args = make([]E, len(raw))
		unknown = make([]bool, len(raw))
	}
	for i, child := range raw {
		argument, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: DirectDeferredArgument})
		if err != nil {
			return nil, nil, err
		}
		if args != nil {
			args[i] = argument.Expression
			unknown[i] = argument.Usage.Type == nil
		}
	}
	return args, unknown, nil
}
