package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type WALPositionKind uint8

const (
	WALCurrentFlush WALPositionKind = iota
	WALCurrentInsert
	WALCurrentWrite
	WALSwitch
	WALRestorePoint
	WALStandbySnapshot
)

var walPositionNames = [...]string{"pg_current_wal_flush_lsn", "pg_current_wal_insert_lsn", "pg_current_wal_lsn", "pg_switch_wal", "pg_create_restore_point", "pg_log_standby_snapshot"}

func (k WALPositionKind) Name() string {
	if int(k) >= len(walPositionNames) {
		return ""
	}
	return walPositionNames[k]
}

// WALPosition retains only the selected routine and its immutable typed operand.
// Physical WAL is not an embedded runtime capability.
type WALPosition struct {
	expr.Info
	Kind WALPositionKind
	Arg expr.Expr
}

func bindWALPosition(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	var inline [1]string
	names := inline[:min(len(raw.Args), 1)]
	if len(raw.Args) > 1 && len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = arg
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
	kind := WALPositionKind(255)
	for i, candidate := range walPositionNames {
		if candidate == name {
			kind = WALPositionKind(i)
			break
		}
	}
	count := 0
	if kind == WALRestorePoint {
		count = 1
	}
	labels := [1]string{""}
	_, valid := namedSlots(raw, labels[:count], count)
	if !visible || kind.Name() == "" || !valid || len(raw.Args) != count || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	node := WALPosition{Kind: kind}
	properties := expr.Properties{Volatility: expr.Volatile}
	var formal [1]sqltype.ID
	if count == 1 {
		formal[0] = sqltype.Text
		target := b.Type(sqltype.Text).Usage()
		if operand.Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(operand.Usage, target, sqltype.Implicit); failure != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
		}
		node.Arg, err = b.CoerceArgument(operand, CoercionRequest{Intent: ExactFormalArgument, Target: target})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, node.Arg.Properties())
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	node.Info, err = b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.PgLSN).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, formal[:count]...)
	return node, err
}

func (n WALPosition) Validate() error {
	if n.Kind.Name() == "" || n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.PgLSN || n.Usage != n.Usage.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid WAL position signature")
	}
	if n.Kind != WALRestorePoint {
		if n.Arg != nil {
			return fmt.Errorf("stuntgres: unused WAL position operand")
		}
		return nil
	}
	if n.Arg == nil {
		return fmt.Errorf("stuntgres: missing WAL position operand")
	}
	usage := n.Arg.ResultUsage()
	if usage.Type == nil || usage.Type.ID() != sqltype.Text || usage != usage.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid WAL position operand")
	}
	return nil
}

func (n WALPosition) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	if n.Arg != nil {
		value, err := n.Arg.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			return sqltype.Null(n.Usage.Type), nil
		}
		text, err := value.Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		if len(text) >= 64 {
			return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "value too long for restore point (maximum 63 characters)"}
		}
	}
	return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: n.Kind.Name()+" physical WAL access is unsupported in embedded mode"}
}

func (n WALPosition) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
