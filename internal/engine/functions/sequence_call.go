package functions

import (
	"errors"
	"strconv"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// SequenceCall is nextval, currval, setval or lastval. Ref is the provider
// bound from a captured regclass literal (zero otherwise) and Temporary its
// TEMP provenance. Identity marks an identity column's generated DEFAULT,
// which skips the sequence ACL check and narrows to the column type.
type SequenceCall struct {
	expr.Info
	Name      string
	Args      []expr.Expr
	Ref       ids.ObjectRef
	Temporary ids.SessionToken
	Identity  bool
}

func (x SequenceCall) Eval(ctx expr.Context) (sqltype.Value, error) {
	if ctx == nil {
		return sqltype.Value{}, errors.New("stuntgres: missing expression context")
	}
	runtime, ok := ctx.(SequenceRuntime)
	if !ok {
		return sqltype.Value{}, errors.New("stuntgres: sequence call lacks statement authority")
	}
	if err := runtime.SequenceAuthority(); err != nil {
		return sqltype.Value{}, err
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
	request := SequenceRequest{Name: x.Name, Ref: x.Ref, Temporary: x.Temporary, Identity: x.Identity}
	if x.Name != "lastval" && x.Ref.Domain() == nil {
		text, err := values[0].Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		// Literal and imported parameter datums already contain canonical OIDs.
		// Never resolve a name here or retarget input after argument effects.
		oid, err := strconv.ParseUint(text, 10, 32)
		if err != nil {
			return sqltype.Value{}, err
		}
		request.OID = ids.OID(oid)
	}
	if x.Name == "setval" {
		request.Value = values[1]
		if len(x.Args) == 3 {
			request.Called, request.HasCalled = values[2], true
		}
	}
	result, err := runtime.Sequence(request)
	if err != nil {
		return sqltype.Value{}, err
	}
	if x.Identity {
		switch x.Usage.Type.ID() {
		case sqltype.Int2:
			result = int64(int16(result))
		case sqltype.Int4:
			result = int64(int32(result))
		}
	}
	return sqltype.Integer(x.Usage.Type, result)
}

// BindSequenceCall binds the sequence-function syntax (parser.CallExpr). It
// is reached directly from expression dispatch, never through the registry,
// so no shared call gates apply.
func BindSequenceCall(b Binder, r parser.CallExpr) (expr.Expr, error) {
	name := r.Name.Parts[len(r.Name.Parts)-1].Text
	if b.DefaultConsumer() && (name != "nextval" || len(r.Args) != 1) {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", r.SourcePosition())
	}
	visible, err := b.BuiltinVisible(r.Name)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, sequenceSignatureError(b, r)
	}
	count := len(r.Args)
	want := 1
	if name == "lastval" {
		want = 0
	}
	if name == "setval" {
		want = 2
	}
	if count != want && !(name == "setval" && count == 3) {
		return nil, sequenceSignatureError(b, r)
	}
	args := make([]expr.Expr, count)
	properties := expr.Properties{Volatility: expr.Stable}
	if name == "nextval" || name == "setval" {
		properties.Volatility = expr.Volatile
	}
	var ref ids.ObjectRef
	for i, raw := range r.Args {
		var node expr.Expr
		var err error
		if i == 0 {
			node, ref, err = b.SequenceArgument(raw)
		} else {
			id := sqltype.Int8
			if i == 2 {
				id = sqltype.Bool
			}
			node, err = b.CoerceArgument(Argument{Raw: raw}, CoercionRequest{Intent: DirectFormalArgument, Target: b.Type(id).Usage()})
		}
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok && (d.Code == "42804" || d.Code == "42846") {
				return nil, sequenceSignatureError(b, r)
			}
			return nil, err
		}
		args[i] = node
		properties.ParameterDependent = properties.ParameterDependent || node.Properties().ParameterDependent
		properties.RowDependent = properties.RowDependent || node.Properties().RowDependent
		if node.Properties().Volatility > properties.Volatility {
			properties.Volatility = node.Properties().Volatility
		}
	}
	temporary, err := b.SequenceTemporary(ref, r.SourcePosition())
	if err != nil {
		return nil, err
	}
	formal := [3]sqltype.ID{sqltype.Regclass, sqltype.Int8, sqltype.Bool}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Int8).Usage(), r.SourcePosition(), properties), SequenceRoutineProvider, name, formal[:count]...)
	if err != nil {
		return nil, err
	}
	for _, arg := range args {
		if value, ok := b.ConstantValue(arg); ok && value.IsNull() {
			// PostgreSQL simplifies these four strict builtins with a known NULL
			// input even when another input is volatile. Keep analysis captures
			// and parameter types, but never evaluate the eliminated argument tree.
			return b.Literal(sqltype.Null(b.Type(sqltype.Int8)), r.SourcePosition()), nil
		}
	}
	return SequenceCall{Info: info, Name: name, Args: args, Ref: ref, Temporary: temporary}, nil
}

// sequenceSignatureError names written literals by their syntax, as
// PostgreSQL's parser types them, and other arguments by their analyzed type.
// Probe failures leave the name unknown. The signature is not noted.
func sequenceSignatureError(b Binder, r parser.CallExpr) error {
	names := make([]string, len(r.Args))
	for i, raw := range r.Args {
		names[i] = "unknown"
		switch v := raw.(type) {
		case parser.RegclassExpr:
			names[i] = "regclass"
		case parser.TypedLiteralExpr:
			names[i] = v.TypeName.Name
		case parser.LiteralExpr:
			switch v.Value.Kind {
			case parser.BitStringValue:
				names[i] = "bit"
			case parser.BooleanValue:
				names[i] = "boolean"
			case parser.NumericValue:
				names[i] = "numeric"
			case parser.IntegerValue:
				names[i] = "integer"
				if v.Value.Integer < -2147483648 || v.Value.Integer > 2147483647 {
					names[i] = "bigint"
				}
			}
		default:
			if argument, err := b.AnalyzeArgument(raw, AnalysisRequest{Intent: BareProjectionArgument}); err == nil && argument.Expression != nil && argument.Expression.ResultUsage().Type != nil {
				names[i] = argument.Expression.ResultUsage().Type.DiagnosticName()
			}
		}
	}
	return UnnotedResolutionError(b, r.Name, r.SourcePosition(), names, false)
}
