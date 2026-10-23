package functions

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var cubeSpatialNames = [...]string{"cube_overlap", "cube_contains", "cube_contained", "cube_distance", "distance_taxicab", "distance_chebyshev"}

// InstalledCubeRoutine exposes the actual installed scalar descriptor. Empty
// signature resolution is specific to this family, not wildcard overload lookup.
type InstalledCubeRoutine interface {
	InstalledRoutine
	CubeType() *sqltype.Descriptor
}

func bindCubeSpatial(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [2]Argument
	var nameStorage [2]string
	names := nameStorage[:min(len(raw.Args), len(nameStorage))]
	if len(raw.Args) > len(nameStorage) {
		names = make([]string, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
		name := "unknown"
		if o.Usage.Type != nil {
			name = b.DiagnosticTypeName(o.Usage.Type)
		}
		names[i] = NamedArgumentType(raw, i, name)
	}
	if err := CallLabelError(raw); err != nil {
		return nil, err
	}
	routine, err := b.InstalledRoutine(raw.Name, CubeExtension, "")
	if err != nil {
		return nil, err
	}
	installed, ok := routine.(InstalledCubeRoutine)
	name := strings.ToLower(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	op, selected := sqltype.CubeSpatialKind(name)
	if !ok || !selected || len(raw.Args) != 2 || raw.Variadic || len(raw.ArgNames) != 0 || raw.Distinct || raw.All || raw.Star || raw.Window != nil || raw.Filter != nil || len(raw.Order) != 0 {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := installed.Validate(); err != nil {
		return nil, err
	}
	target := installed.CubeType().Usage()
	var args [2]expr.Expr
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, o := range operands {
		if o.Usage.Type != nil && !b.CanCoerce(o.Usage, target, sqltype.Implicit) {
			return nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
		args[i], err = b.CoerceArgument(o, CoercionRequest{Intent: ConditionalArgument, Target: target, Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	call, err := NewCubeSpatial(b.Info(b.Type(op.ResultID()).Usage(), raw.Pos, properties), op, args)
	if err != nil {
		return nil, err
	}
	return installed.Attach(call)
}
