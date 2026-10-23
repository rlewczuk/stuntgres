package functions

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func CubeFunctionName(name string) bool {
	return name == "cube" || name == "cube_dim" || name == "cube_is_point" || name == "cube_ll_coord" || name == "cube_ur_coord" || name == "cube_coord" || name == "cube_coord_llur"
}

// CubeFormals is the complete selected family, in release-documentation order.
// Cube is the actual installed descriptor; builtin formals come from the same
// binding catalog, never a process-global descriptor generation.
func CubeFormals(operation sqltype.CubeOperation, cube, floating, array, integer *sqltype.Descriptor) ([3]*sqltype.Descriptor, int) {
	switch operation {
	case sqltype.CubeScalar:
		return [3]*sqltype.Descriptor{floating}, 1
	case sqltype.CubeScalars:
		return [3]*sqltype.Descriptor{floating, floating}, 2
	case sqltype.CubeArray:
		return [3]*sqltype.Descriptor{array}, 1
	case sqltype.CubeArrays:
		return [3]*sqltype.Descriptor{array, array}, 2
	case sqltype.CubeExtend:
		return [3]*sqltype.Descriptor{cube, floating}, 2
	case sqltype.CubeExtendPair:
		return [3]*sqltype.Descriptor{cube, floating, floating}, 3
	case sqltype.CubeDimension, sqltype.CubeIsPoint:
		return [3]*sqltype.Descriptor{cube}, 1
	case sqltype.CubeLowerCoordinate, sqltype.CubeUpperCoordinate, sqltype.CubeRawCoordinate, sqltype.CubeSignedCoordinate:
		return [3]*sqltype.Descriptor{cube, integer}, 2
	}
	return [3]*sqltype.Descriptor{}, 0
}

// Cube is an already-selected call. The plan wrapper owns procedure authority.
type Cube struct {
	expr.Info
	Operation sqltype.CubeOperation
	CubeType  *sqltype.Descriptor
	Args      []expr.Expr
}

func (e Cube) Name() string {
	switch e.Operation {
	case sqltype.CubeDimension:
		return "cube_dim"
	case sqltype.CubeIsPoint:
		return "cube_is_point"
	case sqltype.CubeLowerCoordinate:
		return "cube_ll_coord"
	case sqltype.CubeUpperCoordinate:
		return "cube_ur_coord"
	case sqltype.CubeRawCoordinate:
		return "cube_coord"
	case sqltype.CubeSignedCoordinate:
		return "cube_coord_llur"
	}
	return "cube"
}

func (e Cube) Signature() string {
	var parts [3]string
	for i, arg := range e.Args {
		parts[i] = strconv.FormatUint(uint64(arg.ResultUsage().Type.ID()), 10)
	}
	return strings.Join(parts[:len(e.Args)], ",")
}

func NewCube(info expr.Info, operation sqltype.CubeOperation, cube *sqltype.Descriptor, args []expr.Expr) (Cube, error) {
	e := Cube{Info: info, Operation: operation, CubeType: cube, Args: args}
	return e, e.Validate()
}

func (e Cube) Validate() error {
	if !e.CubeType.IsCube() || e.Usage.Type == nil || len(e.Args) == 0 || len(e.Args) > 3 {
		return fmt.Errorf("stuntgres: invalid cube call")
	}
	result := e.CubeType
	if e.Operation >= sqltype.CubeDimension {
		id := sqltype.Float8
		if e.Operation == sqltype.CubeDimension {
			id = sqltype.Int4
		} else if e.Operation == sqltype.CubeIsPoint {
			id = sqltype.Bool
		}
		if e.Usage.Type.ID() != id {
			return fmt.Errorf("stuntgres: invalid cube inquiry result")
		}
	} else if e.Usage.Type != result {
		return fmt.Errorf("stuntgres: foreign cube result generation")
	}
	formals, n := CubeFormals(e.Operation, e.CubeType, nil, nil, nil)
	if n == 0 || n != len(e.Args) {
		return fmt.Errorf("stuntgres: invalid cube call arity")
	}
	for i, arg := range e.Args {
		if arg == nil {
			return fmt.Errorf("stuntgres: missing cube operand")
		}
		t := arg.ResultUsage().Type
		if t == nil || formals[i] != nil && t != formals[i] {
			return fmt.Errorf("stuntgres: foreign cube formal generation")
		}
		if formals[i] == nil {
			if e.Operation == sqltype.CubeArray || e.Operation == sqltype.CubeArrays {
				if !t.IsArray() || t.Element().ID() != sqltype.Float8 {
					return fmt.Errorf("stuntgres: invalid cube array formal")
				}
			} else if e.Operation >= sqltype.CubeLowerCoordinate {
				if t.ID() != sqltype.Int4 {
					return fmt.Errorf("stuntgres: invalid cube integer formal")
				}
			} else if t.ID() != sqltype.Float8 {
				return fmt.Errorf("stuntgres: invalid cube float formal")
			}
		}
	}
	return nil
}

func (e Cube) Children() []expr.Expr { return e.Args }
func (e Cube) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Cube", children, len(e.Args)); err != nil {
		return nil, err
	}
	return rebuilt(NewCube(info, e.Operation, e.CubeType, children))
}

func (e Cube) ValidateStoredProvider() error        { return e.Validate() }
func (e Cube) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e Cube) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	cube := env.Type(e.CubeType)
	changed = changed || cube != e.CubeType
	e.CubeType = cube
	next, err := e.Rebuild(info, children)
	return next, changed, err
}

func (e Cube) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}

func (e Cube) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var args [3]sqltype.Value
	null := false
	for i, child := range e.Args {
		args[i], err = child.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		null = null || args[i].IsNull()
	}
	if null {
		return sqltype.Null(e.Usage.Type), nil
	}
	if e.Operation < sqltype.CubeDimension {
		return sqltype.ConstructCube(ctx, e.CubeType, e.Operation, args[:len(e.Args)])
	}
	if e.Operation >= sqltype.CubeLowerCoordinate {
		index, err := args[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		coordinate, err := sqltype.CubeCoordinate(ctx, e.CubeType, args[0], e.Operation, int32(index))
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Float(e.Usage.Type, coordinate)
	}
	dimension, point, err := sqltype.CubeInfo(e.CubeType, args[0])
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Operation == sqltype.CubeDimension {
		return sqltype.Integer(e.Usage.Type, int64(dimension))
	}
	return sqltype.Boolean(e.Usage.Type, point), nil
}
