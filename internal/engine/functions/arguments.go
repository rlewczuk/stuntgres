package functions

import (
	"stuntgres/internal/engine/expr"
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
type Argument struct {
	Expression expr.Expr
	Raw        parser.Expr
	Usage      sqltype.Usage
	Kind       ArgumentKind
	// TypeInquiryUnknown is analyzer-confirmed logical UNKNOWN provenance,
	// including an existing successful pseudo-cast no-op.
	TypeInquiryUnknown *TypeInquiryUnknownArgument
	// TypeInquiryStringIO authenticates a real explicit string-output to
	// UNKNOWN cast; Expression owns its concrete IO transport and source.
	TypeInquiryStringIO *sqltype.Descriptor
}

type TypeInquiryUnknownArgument struct {
	Literal  parser.LiteralExpr
	CastType *sqltype.Descriptor
	Raw      parser.Expr
	Chain    []expr.TransparentInputCast
}

type AnalysisIntent uint8

const (
	ComparisonArgument AnalysisIntent = iota
	DirectDeferredArgument
	StringTextArgument
	StringOtherArgument
	StringProjectionArgument
	ProjectionArgument
	// ThresholdArrayArgument is a comparison operand whose unknown-NULL
	// annotation may name an enum array, as width_bucket's thresholds may.
	ThresholdArrayArgument
	// RecordBaseArgument is a string-other operand that is a populate-record
	// base: named casts to user-defined composites keep their occurrence
	// identity.
	RecordBaseArgument
	// ZoneArgument is a comparison operand of timezone/date_add/date_subtract,
	// whose explicit casts admit the zone family's temporal targets.
	ZoneArgument
	// BareProjectionArgument binds the operand in projection context without
	// the unknown-NULL annotation, as aggregate arguments are bound.
	BareProjectionArgument
	// TypeInquiryArgument preserves direct unknown literals and the logical
	// witness of the analyzer's already-admitted pseudo-cast no-op branch.
	TypeInquiryArgument
)

type AnalysisRequest struct{ Intent AnalysisIntent }
type ArgumentAnalyzer interface {
	AnalyzeArgument(parser.Expr, AnalysisRequest) (Argument, error)
}

// OriginalArgument is an optional, pure syntax-recovery seam for an analyzer's
// private already-bound carrier. Binding continues to use the original input
// carrier, never the recovered recipe, so operands are not analyzed twice.
func OriginalArgument(b any, raw parser.Expr) (parser.Expr, bool) {
	if source, ok := b.(interface {
		OriginalArgument(parser.Expr) (parser.Expr, bool)
	}); ok {
		return source.OriginalArgument(raw)
	}
	return raw, false
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

// Offset windows have anyelement value overloads at arity one/two, and
// anycompatible value/default at arity three. Common-type choice is binder-owned.
func ResolveWindowOffset(visible, star bool, count int, value, offset, integer sqltype.Usage) (Resolution, sqltype.CoercionPlan) {
	var plan sqltype.CoercionPlan
	if !visible || star || count < 1 || count > 3 {
		return Missing, plan
	}
	if count > 1 && offset.Type != nil {
		var err error
		plan, err = sqltype.PlanCoercion(offset, integer, sqltype.Implicit)
		if err != nil {
			return Missing, plan
		}
	}
	if count < 3 && !value.Type.Concrete() {
		return UnknownPolymorphic, plan
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
	// RecordFormalArgument finalizes an operand of a fixed RECORD formal: a
	// parameter keeps its inferred record type, others get an anonymous record.
	RecordFormalArgument
	// AssignmentFormalArgument binds the unanalyzed Raw argument in
	// assignment context to Target with implicit coercion, as the
	// statement-level setting calls bind their literal and parameter inputs.
	AssignmentFormalArgument
	// JSONFormattedArgument applies the planned explicit FORMAT JSON input
	// conversion (Plan, Planned) to the bound Expression, as an explicit cast
	// to Target.
	JSONFormattedArgument
	// TypeNameCastArgument applies the selected type-name conversion through
	// the existing explicit cast node, with planned coercion and provenance.
	TypeNameCastArgument
	// BareFormalArgument applies the planned implicit conversion (Plan,
	// Planned) to an already finalized Expression, positioned at the raw
	// argument and without a cast routine identity, as pg_get_acl and the
	// access inquiries coerce their fixed formals.
	BareFormalArgument
)

type CoercionRequest struct {
	Intent  CoercionIntent
	Target  sqltype.Usage
	Plan    sqltype.CoercionPlan
	Planned bool
	// TypeNameCallPosition identifies the selected ordinary typename call.
	// Argument.Raw retains its original operand for stored cast reconstruction.
	TypeNameCallPosition parser.Position
	// PreserveProperties retains analyzed argument flags instead of deriving
	// conversion flags. BareFormalArgument still omits cast routine provenance.
	PreserveProperties bool
	AtRaw              bool
	Label              string
}
type ArgumentCoercer interface {
	CoerceArgument(Argument, CoercionRequest) (expr.Expr, error)
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

func AnalyzeDirectArguments(b ArgumentAnalyzer, raw []parser.Expr) ([]expr.Expr, []bool, error) {
	var args []expr.Expr
	var unknown []bool
	if len(raw) <= 100 {
		args = make([]expr.Expr, len(raw))
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
