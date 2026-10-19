package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func JSONCreateKind(name string) sqltype.JSONCreateKind {
	switch strings.ToLower(name) {
	case "to_json":
		return sqltype.ToJSON
	case "to_jsonb":
		return sqltype.ToJSONB
	case "array_to_json":
		return sqltype.ArrayToJSON
	case "row_to_json":
		return sqltype.RowToJSON
	case "json_build_array":
		return sqltype.JSONBuildArray
	case "jsonb_build_array":
		return sqltype.JSONBBuildArray
	case "json_build_object":
		return sqltype.JSONBuildObject
	case "jsonb_build_object":
		return sqltype.JSONBBuildObject
	}
	return 0
}

type JSONCreate struct {
	expr.Info
	Kind     sqltype.JSONCreateKind
	Plan     sqltype.JSONCreatePlan
	Args     []expr.Expr
	Variadic bool
	Unknown  [2]uint64
}

func (e JSONCreate) UnknownArgument(i int) bool {
	return e.Unknown[i/64]&(uint64(1)<<uint(i%64)) != 0
}
func (e JSONCreate) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var scratch [100]sqltype.Value
	if len(e.Args) > len(scratch) {
		return sqltype.Value{}, fmt.Errorf("invalid JSON creation expression arity")
	}
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		scratch[i] = value
	}
	return e.Plan.Apply(ctx, scratch[:len(e.Args)], e.Variadic && e.Kind.Builder(), c.FunctionJSONOutput())
}
func (e JSONCreate) Reconstruct(info expr.Info, args []expr.Expr) (JSONCreate, error) {
	var local [100]sqltype.Usage
	if len(args) > len(local) {
		return e, fmt.Errorf("invalid JSON creation expression arity")
	}
	for i, arg := range args {
		local[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindJSONCreate(e.Kind, info.Usage, local[:len(args)]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// JSONCreate implements expr.Composite.
var _ expr.Composite = JSONCreate{}

func (e JSONCreate) Children() []expr.Expr { return e.Args }
func (e JSONCreate) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONCreate", children, len(e.Args)); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children))
}
func NewJSONCreate(info expr.Info, kind sqltype.JSONCreateKind, args []expr.Expr, variadic bool, unknown [2]uint64) (JSONCreate, error) {
	expression := JSONCreate{Info: info, Kind: kind, Args: args, Variadic: variadic, Unknown: unknown}
	return expression.Reconstruct(info, args)
}

func ResolveJSONCreate(kind sqltype.JSONCreateKind, name string, visible, variadic, resolutionOnly bool, names int, args []Argument, boolean sqltype.Usage) Resolution {
	valid := visible && name == kind.Name() && !resolutionOnly && names == 0
	if kind.Builder() {
		valid = valid && (!variadic || len(args) == 1)
	} else {
		valid = valid && len(args) >= 1 && len(args) <= 2
		if kind == sqltype.ToJSON || kind == sqltype.ToJSONB {
			valid = valid && len(args) == 1
		}
		if len(args) > 0 && args[0].Usage.Type != nil {
			base := args[0].Usage.Type.BaseType()
			if kind == sqltype.ArrayToJSON {
				valid = valid && base.IsArray()
			}
			if kind == sqltype.RowToJSON {
				valid = valid && base.IsComposite()
			}
		}
		if len(args) == 2 && args[1].Usage.Type != nil {
			_, err := sqltype.PlanCoercion(args[1].Usage, boolean, sqltype.Implicit)
			valid = valid && err == nil
		}
	}
	if !valid {
		return Missing
	}
	return Resolved
}

type JSONCreateArgumentRule uint8

const (
	JSONKeepArgument JSONCreateArgumentRule = iota
	JSONUnknownPolymorphic
	JSONRecordFormal
	JSONBooleanFormal
	JSONTextUnknown
)

func JSONCreateRawUnknown(raw parser.Expr) bool {
	literal, ok := raw.(parser.LiteralExpr)
	return ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue)
}
func JSONCreateRule(kind sqltype.JSONCreateKind, index int, usage sqltype.Usage, rawUnknown bool) JSONCreateArgumentRule {
	if !kind.Builder() && index == 0 && usage.Type == nil {
		if kind != sqltype.RowToJSON {
			return JSONUnknownPolymorphic
		}
		return JSONRecordFormal
	}
	if !kind.Builder() && index == 1 {
		return JSONBooleanFormal
	}
	if kind.Builder() && usage.Type == nil && rawUnknown {
		return JSONTextUnknown
	}
	return JSONKeepArgument
}
func JSONCreateResult(kind sqltype.JSONCreateKind) sqltype.ID {
	if kind == sqltype.ToJSONB || kind == sqltype.JSONBBuildArray || kind == sqltype.JSONBBuildObject {
		return sqltype.JSONB
	}
	return sqltype.JSON
}

type LabelFailureKind uint8

const (
	LabelsValid LabelFailureKind = iota
	PositionalAfterNamed
	DuplicateLabel
)

type LabelFailure struct {
	Kind LabelFailureKind
	Name string
	Pos  parser.Position
}

func AnalyzeLabels(labels []parser.Identifier, args []parser.Expr) LabelFailure {
	named := false
	for i, label := range labels {
		if label.Text == "" {
			if named {
				return LabelFailure{Kind: PositionalAfterNamed, Pos: args[i].SourcePosition()}
			}
			continue
		}
		named = true
		for _, previous := range labels[:i] {
			if previous.Text == label.Text {
				return LabelFailure{Kind: DuplicateLabel, Name: label.Text, Pos: label.Pos}
			}
		}
	}
	return LabelFailure{}
}

func JSONCreateIntrinsicRule() expr.IntrinsicPropertyRule { return expr.ExactStable }
func JSONCreateVariadicArgumentValid(usage sqltype.Usage) bool {
	return usage.Type != nil && usage.Type.BaseType().IsArray()
}

func (e JSONCreate) IntrinsicVolatility() expr.Volatility { return expr.Stable }

// PlanningFold: all eight JSON creation functions are intrinsically STABLE.
func (e JSONCreate) PlanningFold() expr.Fold { return expr.FoldNever }

func (e JSONCreate) ValidateStoredProvider() error { return nil }

var _ expr.Renderable = JSONCreate{}

// Render implements expr.Renderable.
func (e JSONCreate) Render() (expr.Rendering, error) {
	spec := variadicRendering(fmt.Sprintf("JSONCreate %s variadic %t", e.Kind.Name(), e.Variadic), e.Kind.Name(), e.Args, e.Variadic)
	spec.UnknownTexts = true
	for i := range spec.Args {
		spec.Args[i].UnknownText = e.UnknownArgument(i)
		if !spec.Args[i].UnknownText {
			spec.Args[i].Transport = expr.TransportLiteral
		}
	}
	spec.Replay.CatalogSnapshot = catalogArguments(e.Args)
	return spec, nil
}

// SameGroup implements expr.GroupIdentity.
func (e JSONCreate) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(JSONCreate)
	if !ok || e.Kind != o.Kind || e.Variadic != o.Variadic || len(e.Args) != len(o.Args) {
		return false
	}
	for i := range e.Args {
		if !same(e.Args[i], o.Args[i]) {
			return false
		}
	}
	return true
}

// PreflightFold implements expr.Preflighting.
func (e JSONCreate) PreflightFold() expr.Preflight { return expr.PreflightChildren }

// LooksLikeFunction implements expr.FunctionSyntax.
func (e JSONCreate) LooksLikeFunction() bool { return true }

// Substitute implements expr.Substitutable.
// Its provider re-derives from the child usages, as preflight always did.
func (e JSONCreate) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
