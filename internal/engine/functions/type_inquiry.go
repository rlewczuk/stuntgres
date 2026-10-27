package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type TypeInquiryKind uint8

const (
	FormatTypeInquiry TypeInquiryKind = iota
	BaseTypeInquiry
	TypeofInquiry
)

// TypeInquiry retains logical argument identity independently of the concrete
// transport of an untyped literal. Neither identity nor output needs a datum
// lookup, but every demanded operand still executes.
type TypeInquiry struct {
	expr.Info
	Kind           TypeInquiryKind
	Args           []expr.Expr
	logical        sqltype.ID
	unknown        *typeInquiryUnknown
	stringIO       *sqltype.Descriptor // Authenticated logical UNKNOWN target on a real IO transport.
	stringIOSyntax parser.CastExpr
	stringIOChain  []expr.TransparentInputCast
	result         *sqltype.Descriptor
	formals        [2]*sqltype.Descriptor
	routine        expr.RoutineReference
}

type typeInquiryUnknown struct {
	raw       parser.LiteralExpr
	transport expr.Expr
	castType  *sqltype.Descriptor
	source    parser.Expr
}

func (n TypeInquiry) Eval(ctx expr.Context) (sqltype.Value, error) {
	if ctx == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing expression context")
	}
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range n.Args {
		value, err := arg.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	if n.Kind == TypeofInquiry {
		return sqltype.RegtypeValue(n.Usage.Type, uint32(n.logical))
	}
	if values[0].IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	runtime, err := catalogInquiryRuntime(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.Kind == BaseTypeInquiry {
		id, err := values[0].TypeOID()
		if err != nil {
			return sqltype.Value{}, err
		}
		return runtime.BaseType(id, n.Usage.Type)
	}
	id, err := values[0].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	var modifier int64
	specified := !values[1].IsNull()
	if specified {
		modifier, err = values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return runtime.FormatType(id, int32(modifier), specified, n.Usage.Type)
}
