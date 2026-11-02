package functions

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var earthCoordinateNames = [...]string{"earth", "sec_to_gc", "gc_to_sec", "ll_to_earth", "latitude", "longitude", "earth_distance", "earth_box"}

func EarthFunctionName(name string) bool {
	if name == "geo_distance" {
		return true
	}
	for _, candidate := range earthCoordinateNames {
		if name == candidate {
			return true
		}
	}
	return false
}

type EarthRoutine interface {
	InstalledRoutine
	EarthDescriptor() *sqltype.Descriptor
}

func bindEarthCoordinate(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var storage [2]Argument
	operands := storage[:min(len(raw.Args), len(storage))]
	if len(raw.Args) > len(storage) {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = o
	}
	if err := CallLabelError(raw); err != nil {
		return nil, err
	}
	routine, err := b.InstalledRoutine(raw.Name, EarthdistanceExtension, "")
	if err != nil {
		return nil, err
	}
	selected, ok := routine.(EarthRoutine)
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	op := sqltype.EarthOperation(255)
	for candidate := sqltype.EarthRadiusOperation; candidate <= sqltype.EarthBox; candidate++ {
		if candidate.Name() == name {
			op = candidate
			break
		}
	}
	if !ok || op.Arity() != len(raw.Args) || raw.Variadic || raw.Star || len(raw.ArgNames) != 0 {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if err := selected.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	kernel, err := sqltype.BindEarthCoordinate(op, selected.EarthDescriptor(), b.Type(sqltype.Float8))
	if err != nil {
		return nil, err
	}
	args := make([]expr.Expr, len(operands))
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, o := range operands {
		target := kernel.Formal(i).Usage()
		if o.Usage.Type != nil && !b.CanCoerce(o.Usage, target, sqltype.Implicit) {
			return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
		}
		args[i], err = b.CoerceArgument(o, CoercionRequest{Intent: ConditionalArgument, Target: target, Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	call, err := NewEarthCoordinate(b.Info(kernel.Result().Usage(), raw.Pos, properties), op, kernel.Earth(), args)
	if err != nil {
		return nil, err
	}
	return selected.Attach(call)
}

type EarthCoordinate struct {
	expr.Info
	Plan                sqltype.EarthCoordinatePlan
	Args                []expr.Expr
	BoxContext          EarthBoxContext
	BoxConversionDemand bool
}

type EarthBoxContext uint8

const (
	EarthBoxHelperOnly EarthBoxContext = iota
	EarthBoxStatement
	EarthBoxInlining
)

// EarthCoordinateRuntime exposes already-captured command policy. Diagnostic
// names are resolved from checked current declarations only after body failure.
type EarthCoordinateRuntime interface {
	EarthCoordinatePreserveParameterDemand() bool
	EarthCoordinateDiagnosticNames(EarthCoordinate) (routine, helper string, err error)
}

func NewEarthCoordinate(info expr.Info, op sqltype.EarthOperation, earth *sqltype.Descriptor, args []expr.Expr) (EarthCoordinate, error) {
	floating := info.Usage.Type
	if op == sqltype.EarthLL || op == sqltype.EarthBox {
		index := 0
		if op == sqltype.EarthBox {
			index = 1
		}
		if len(args) != 2 || args[index] == nil {
			return EarthCoordinate{}, fmt.Errorf("invalid earth coordinate arity")
		}
		floating = args[index].ResultUsage().Type
	}
	p, err := sqltype.BindEarthCoordinate(op, earth, floating)
	if err != nil {
		return EarthCoordinate{}, err
	}
	e := EarthCoordinate{Info: info, Plan: p, Args: args}
	if info.Usage.Type != p.Result() || len(args) != op.Arity() {
		return e, fmt.Errorf("invalid earth coordinate result/arity")
	}
	for i, arg := range args {
		if arg == nil || arg.ResultUsage().Type != p.Formal(i) {
			return e, fmt.Errorf("foreign earth coordinate formal")
		}
	}
	if op == sqltype.EarthBox {
		first, radius := args[0].Properties(), args[1].Properties()
		statement := first.ParameterDependent && first.Volatility == expr.Immutable && !first.RowDependent && !first.StageDependent
		// Parameter-only earth retains the strict statement call. Expanded
		// literal/row bodies demand gc_to_sec independently of cube's NULL gate.
		e.BoxConversionDemand = !statement
		if statement {
			e.BoxContext = EarthBoxStatement
		} else if first.Volatility == expr.Immutable && !first.RowDependent && !first.StageDependent ||
			first.RowDependent && radius.Volatility == expr.Immutable && !radius.RowDependent && !radius.StageDependent {
			e.BoxContext = EarthBoxInlining
		}
	}
	return e, nil
}

// BoxExecutionRoute combines immutable argument provenance with this command's
// demand policy; it never changes the analyzed/stored carrier or samples settings.
func (e EarthCoordinate) BoxExecutionRoute(preserveParameterDemand bool) (bool, EarthBoxContext) {
	if e.Plan.Operation() == sqltype.EarthBox && !e.BoxConversionDemand && preserveParameterDemand {
		return true, EarthBoxHelperOnly
	}
	return e.BoxConversionDemand, e.BoxContext
}
func (e EarthCoordinate) Name() string { return e.Plan.Operation().Name() }
func (e EarthCoordinate) Signature() string {
	var parts [2]string
	for i, arg := range e.Args {
		parts[i] = strconv.FormatUint(uint64(arg.ResultUsage().Type.ID()), 10)
	}
	return strings.Join(parts[:len(e.Args)], ",")
}
func (e EarthCoordinate) Validate() error {
	n, err := NewEarthCoordinate(e.Info, e.Plan.Operation(), e.Plan.Earth(), e.Args)
	if err != nil || n.Plan != e.Plan || n.BoxContext != e.BoxContext || n.BoxConversionDemand != e.BoxConversionDemand {
		return fmt.Errorf("invalid earth coordinate expression")
	}
	return nil
}
func (e EarthCoordinate) ValidateStoredProvider() error        { return e.Validate() }
func (e EarthCoordinate) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e EarthCoordinate) Children() []expr.Expr                { return e.Args }
func (e EarthCoordinate) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return rebuilt(NewEarthCoordinate(info, e.Plan.Operation(), e.Plan.Earth(), children))
}
func (e EarthCoordinate) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e EarthCoordinate) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	earth := e.Plan.Earth()
	if earth != nil {
		earth = env.Type(earth)
	}
	n, err := NewEarthCoordinate(info, e.Plan.Operation(), earth, children)
	return n, changed || earth != e.Plan.Earth(), err
}
func (e EarthCoordinate) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Plan.Operation() < sqltype.EarthDistance {
		for _, arg := range e.Args {
			if c.FunctionLiteralNull(arg) {
				return sqltype.Null(e.Usage.Type), nil
			}
		}
	}
	var args [2]sqltype.Value
	for i, child := range e.Args {
		args[i], err = child.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	conversionDemand, boxContext := e.BoxConversionDemand, e.BoxContext
	if e.Plan.Operation() == sqltype.EarthBox && !conversionDemand {
		runtime, ok := c.(EarthCoordinateRuntime)
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: missing earth coordinate runtime")
		}
		conversionDemand, boxContext = e.BoxExecutionRoute(runtime.EarthCoordinatePreserveParameterDemand())
	}
	var value sqltype.Value
	if conversionDemand {
		value, err = e.Plan.EvaluateExpandedBox(ctx, args[:len(e.Args)], c.FunctionInput())
	} else {
		value, err = e.Plan.Evaluate(ctx, args[:len(e.Args)], c.FunctionInput())
	}
	if err != nil {
		runtime, ok := c.(EarthCoordinateRuntime)
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: missing earth coordinate diagnostic runtime")
		}
		name, helper, failure := runtime.EarthCoordinateDiagnosticNames(e)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		context := `SQL function "` + name + `" statement 1`
		if helper != "" {
			prefix := `SQL function "` + helper + `" statement 1`
			if e.Plan.Operation() == sqltype.EarthBox {
				switch boxContext {
				case EarthBoxHelperOnly:
					context = ""
				case EarthBoxInlining:
					context = `SQL function "` + name + `" during inlining`
				}
			}
			if context == "" {
				context = prefix
			} else {
				context = prefix + "\n" + context
			}
		}
		return sqltype.Value{}, OverlapsErrorContext(err, context)
	}
	return value, nil
}
