package functions

import (
	"strconv"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// CubeRoutine carries the installed result type selected by catalog authority.
// The generic Binder interface does not gain a second type/name resolver.
type CubeRoutine interface {
	InstalledRoutine
	CubeDescriptor() *sqltype.Descriptor
}

func bindCube(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [3]Argument
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := CallLabelError(raw); err != nil {
		return nil, err
	}
	if len(raw.Args) < 1 || len(raw.Args) > 3 {
		return nil, b.CallSignatureError(raw)
	}
	args := operands[:len(raw.Args)]
	var parts [3]string
	for i, arg := range args {
		if arg.Usage.Type == nil {
			return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, args))
		}
		parts[i] = strconv.FormatUint(uint64(arg.Usage.Type.ID()), 10)
	}
	routine, err := b.InstalledRoutine(raw.Name, CubeExtension, strings.Join(parts[:len(args)], ","))
	if err != nil {
		return nil, err
	}
	selected, ok := routine.(CubeRoutine)
	if !ok || raw.Variadic || raw.Star || len(raw.ArgNames) != 0 {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, args))
	}
	if err := selected.Validate(); err != nil {
		return nil, err
	}
	cube := selected.CubeDescriptor()
	floating, array := b.Type(sqltype.Float8), b.Type(1022)
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operation, found := sqltype.CubeScalar, false
	for candidate := sqltype.CubeScalar; candidate <= sqltype.CubeIsPoint; candidate++ {
		candidateName := "cube"
		if candidate == sqltype.CubeDimension {
			candidateName = "cube_dim"
		} else if candidate == sqltype.CubeIsPoint {
			candidateName = "cube_is_point"
		}
		formals, n := CubeFormals(candidate, cube, floating, array)
		if name != candidateName || n != len(args) {
			continue
		}
		match := true
		for i, arg := range args {
			match = match && arg.Usage.Type == formals[i]
		}
		if match {
			operation, found = candidate, true
			break
		}
	}
	if !found {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, args))
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	children := make([]expr.Expr, len(args))
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, arg := range args {
		children[i] = arg.Expression
		properties = expr.MergeProperties(properties, arg.Expression.Properties())
	}
	result := cube
	if operation == sqltype.CubeDimension {
		result = b.Type(sqltype.Int4)
	} else if operation == sqltype.CubeIsPoint {
		result = b.Type(sqltype.Bool)
	}
	call, err := NewCube(b.Info(result.Usage(), raw.Pos, properties), operation, cube, children)
	if err != nil {
		return nil, err
	}
	return selected.Attach(call)
}
