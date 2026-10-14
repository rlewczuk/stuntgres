package functions

import (
	"fmt"
	"strings"

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

type JSONCreateContext interface {
	Context
	FunctionJSONOutput() sqltype.OutputEnvironment
}
type JSONCreate[E Expression[C], C JSONCreateContext, P any] struct {
	Info[P]
	Kind     sqltype.JSONCreateKind
	Plan     sqltype.JSONCreatePlan
	Args     []E
	Variadic bool
	Unknown  [2]uint64
}

func (e JSONCreate[E, C, P]) UnknownArgument(i int) bool {
	return e.Unknown[i/64]&(uint64(1)<<uint(i%64)) != 0
}
func (e JSONCreate[E, C, P]) Eval(c C) (sqltype.Value, error) {
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
func (e JSONCreate[E, C, P]) Reconstruct(info Info[P], args []E) (JSONCreate[E, C, P], error) {
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
func NewJSONCreate[E Expression[C], C JSONCreateContext, P any](info Info[P], kind sqltype.JSONCreateKind, args []E, variadic bool, unknown [2]uint64) (JSONCreate[E, C, P], error) {
	expression := JSONCreate[E, C, P]{Info: info, Kind: kind, Args: args, Variadic: variadic, Unknown: unknown}
	return expression.Reconstruct(info, args)
}

func ResolveJSONCreate[E any](kind sqltype.JSONCreateKind, name string, visible, variadic, resolutionOnly bool, names int, args []Argument[E], boolean sqltype.Usage) Resolution {
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

func JSONCreateIntrinsicRule() IntrinsicPropertyRule { return ExactStable }
func JSONCreateVariadicArgumentValid(usage sqltype.Usage) bool {
	return usage.Type != nil && usage.Type.BaseType().IsArray()
}
