package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type ReplicationOriginKind uint8

const (
	ReplicationOriginCreate ReplicationOriginKind = iota
	ReplicationOriginDrop
	ReplicationOriginOID
	ReplicationOriginAdvance
	ReplicationOriginProgress
)

var replicationOriginNames = [...]string{"pg_replication_origin_create", "pg_replication_origin_drop", "pg_replication_origin_oid", "pg_replication_origin_advance", "pg_replication_origin_progress"}

func (k ReplicationOriginKind) Name() string {
	if k > ReplicationOriginProgress {
		return ""
	}
	return replicationOriginNames[k]
}
func (k ReplicationOriginKind) signature() (types [2]sqltype.ID, count int, result sqltype.ID) {
	types[0] = sqltype.Text
	switch k {
	case ReplicationOriginCreate, ReplicationOriginOID:
		return types, 1, sqltype.Oid
	case ReplicationOriginDrop:
		return types, 1, sqltype.Void
	case ReplicationOriginAdvance:
		types[1] = sqltype.PgLSN
		return types, 2, sqltype.Void
	case ReplicationOriginProgress:
		types[1] = sqltype.Bool
		return types, 2, sqltype.PgLSN
	}
	return types, 0, 0
}

// ReplicationOrigin owns an immutable typed invocation, never live authority.
type ReplicationOrigin struct {
	expr.Info
	Kind  ReplicationOriginKind
	Arity uint8
	Args  [2]expr.Expr
}

func bindReplicationOrigin(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [2]Argument
	var inline [2]string
	names := inline[:min(len(raw.Args), 2)]
	if len(raw.Args) > 2 && len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < 2 {
			operands[i] = arg
		}
		if i < len(names) {
			typ := "unknown"
			if arg.Usage.Type != nil {
				typ = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, typ)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := ReplicationOriginKind(255)
	for i, candidate := range replicationOriginNames {
		if candidate == name {
			kind = ReplicationOriginKind(i)
			break
		}
	}
	types, count, result := kind.signature()
	if !visible || kind.Name() == "" || len(raw.Args) != count || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	// Released declarations have no callable argument names.
	var labels [2]string
	_, valid := namedSlots(raw, labels[:count], count)
	if !valid {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	node := ReplicationOrigin{Kind: kind, Arity: uint8(count)}
	properties := expr.Properties{Volatility: node.IntrinsicVolatility()}
	for i := range count {
		target := b.Type(types[i]).Usage()
		if operands[i].Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(operands[i].Usage, target, sqltype.Implicit); failure != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
		}
		arg, err := b.CoerceArgument(operands[i], CoercionRequest{Intent: ExactFormalArgument, Target: target})
		if err != nil {
			return nil, err
		}
		node.Args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	node.Info, err = b.BuiltinRoutineInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, types[:count]...)
	return node, err
}
func (n ReplicationOrigin) Validate() error {
	types, count, result := n.Kind.signature()
	if n.Kind.Name() == "" || n.Usage.Type == nil || n.Usage.Type.ID() != result || n.Usage != n.Usage.Type.Usage() || int(n.Arity) != count {
		return fmt.Errorf("stuntgres: invalid replication origin signature")
	}
	for i := range count {
		if n.Args[i] == nil {
			return fmt.Errorf("stuntgres: missing replication origin operand")
		}
		usage := n.Args[i].ResultUsage()
		if usage.Type == nil || usage != usage.Type.Usage() || usage.Type.ID() != types[i] {
			return fmt.Errorf("stuntgres: invalid replication origin operand")
		}
	}
	for i := count; i < 2; i++ {
		if n.Args[i] != nil {
			return fmt.Errorf("stuntgres: unused replication origin operand")
		}
	}
	return nil
}

type ReplicationOriginRuntime interface {
	ReplicationOrigin(ReplicationOriginKind, [2]sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
}

func (n ReplicationOrigin) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	null := false
	for i := range int(n.Arity) {
		value, err := n.Args[i].Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	runtime, ok := ctx.(ReplicationOriginRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing replication origin runtime")
	}
	return runtime.ReplicationOrigin(n.Kind, values, n.Usage.Type)
}
func (n ReplicationOrigin) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
