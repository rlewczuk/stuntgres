package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type LargeObjectRequest struct {
	Name     string
	OID      ids.OID
	Offset   int64
	Length   int64
	Fragment bool
	Data     string
	Descriptor int32
	Mode int32
}
type LargeObjectResult struct {
	OID  ids.OID
	Data string
	Integer int32
}
type LargeObjectRuntime interface {
	LargeObject(LargeObjectRequest) (LargeObjectResult, error)
}

type LargeObjectCall struct {
	expr.Info
	Name string
	Args []expr.Expr
}

func bindLargeObject(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	formal := [3]sqltype.ID{sqltype.Oid}
	count, result := 1, sqltype.Oid
	switch name {
	case "lo_open":
		count, result = 2, sqltype.Int4
		formal[1] = sqltype.Int4
	case "lo_close":
		formal[0], result = sqltype.Int4, sqltype.Int4
	case "loread":
		count, result = 2, sqltype.Bytea
		formal[0], formal[1] = sqltype.Int4, sqltype.Int4
	case "lowrite":
		count, result = 2, sqltype.Int4
		formal[0], formal[1] = sqltype.Int4, sqltype.Bytea
	case "lo_creat":
		formal[0] = sqltype.Int4
	case "lo_from_bytea":
		count = 2
		formal[1] = sqltype.Bytea
	case "lo_get":
		result = sqltype.Bytea
		if len(raw.Args) == 3 {
			count = 3
			formal[1] = sqltype.Int8
			formal[2] = sqltype.Int4
		}
	case "lo_put":
		count = 3
		formal[1] = sqltype.Int8
		formal[2] = sqltype.Bytea
		result = sqltype.Void
	case "lo_unlink":
		result = sqltype.Int4
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !visible || len(raw.Args) != count || raw.Variadic {
		return nil, b.CallSignatureError(raw)
	}
	args := make([]expr.Expr, count)
	properties := expr.Properties{Volatility: expr.Volatile}
	for i, r := range raw.Args {
		node, err := b.CoerceArgument(Argument{Raw: r}, CoercionRequest{Intent: DirectFormalArgument, Target: b.Type(formal[i]).Usage()})
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && (diagnostic.Code == "42804" || diagnostic.Code == "42846") {
				return nil, b.CallSignatureError(raw)
			}
			return nil, err
		}
		args[i] = node
		properties = expr.MergeProperties(properties, node.Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), LargeObjectRoutineProvider, name, formal[:count]...)
	if err != nil {
		return nil, err
	}
	b.NoteCatalogSnapshot()
	return LargeObjectCall{Info: info, Name: name, Args: args}, nil
}
func (x LargeObjectCall) Eval(ctx expr.Context) (sqltype.Value, error) {
	if ctx == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing expression context")
	}
	var values [3]sqltype.Value
	for i, arg := range x.Args {
		v, err := arg.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	for i := range x.Args {
		if values[i].IsNull() {
			return sqltype.Null(x.Usage.Type), nil
		}
	}
	r := LargeObjectRequest{Name: x.Name, Length: -1}
	if x.Name == "lo_close" || x.Name == "loread" || x.Name == "lowrite" {
		fd, err := values[0].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		r.Descriptor = int32(fd)
	} else if x.Name != "lo_creat" {
		oid, err := values[0].OID()
		if err != nil {
			return sqltype.Value{}, err
		}
		r.OID = ids.OID(oid)
	}
	if x.Name == "lo_open" || x.Name == "loread" {
		n, err := values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		if x.Name == "lo_open" {
			r.Mode = int32(n)
		} else {
			r.Length = n
		}
	}
	if x.Name == "lo_put" || x.Name == "lo_get" && len(x.Args) == 3 {
		var err error
		r.Offset, err = values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if x.Name == "lo_get" && len(x.Args) == 3 {
		var err error
		r.Fragment = true
		r.Length, err = values[2].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if x.Name == "lo_from_bytea" || x.Name == "lo_put" || x.Name == "lowrite" {
		i := 1
		if x.Name == "lo_put" {
			i = 2
		}
		var err error
		r.Data, err = values[i].Binary()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	runtime, ok := ctx.(LargeObjectRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: large object call lacks statement authority")
	}
	out, err := runtime.LargeObject(r)
	if err != nil {
		return sqltype.Value{}, err
	}
	switch x.Name {
	case "lo_get", "loread":
		return sqltype.Binary(x.Usage.Type, out.Data)
	case "lo_put":
		return sqltype.VoidValue(x.Usage.Type), nil
	case "lo_unlink":
		return sqltype.Integer(x.Usage.Type, 1)
	case "lo_open", "lo_close", "lowrite":
		return sqltype.Integer(x.Usage.Type, int64(out.Integer))
	default:
		return sqltype.OIDValue(x.Usage.Type, uint32(out.OID))
	}
}
