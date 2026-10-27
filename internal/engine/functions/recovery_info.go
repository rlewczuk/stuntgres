package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// RecoveryInfoKind selects a stateless inquiry in an engine without physical recovery.
type RecoveryInfoKind uint8

const (
	RecoveryInProgress RecoveryInfoKind = iota
	RecoveryReceiveLSN
	RecoveryReplayLSN
	RecoveryReplayTimestamp
)

var recoveryInfoNames = [...]string{"pg_is_in_recovery", "pg_last_wal_receive_lsn", "pg_last_wal_replay_lsn", "pg_last_xact_replay_timestamp", "pg_get_wal_resource_managers"}

func (k RecoveryInfoKind) Name() string {
	if k > RecoveryReplayTimestamp {
		return ""
	}
	return recoveryInfoNames[k]
}

func (k RecoveryInfoKind) result() sqltype.ID {
	switch k {
	case RecoveryInProgress:
		return sqltype.Bool
	case RecoveryReceiveLSN, RecoveryReplayLSN:
		return sqltype.PgLSN
	case RecoveryReplayTimestamp:
		return sqltype.Timestamptz
	}
	return 0
}

// RecoveryInfo retains only a selected immutable typed recipe, never WAL progress.
type RecoveryInfo struct {
	expr.Info
	Kind RecoveryInfoKind
}

// WALResourceManagers retains its fixed OUT shape, not a physical registry.
type WALResourceManagers struct {
	expr.Info
}

func bindRecoveryInfo(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var names []string
	if len(raw.Args) > 0 && len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(names) {
			name := "unknown"
			if arg.Usage.Type != nil {
				name = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, name)
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
	index := -1
	for i, candidate := range recoveryInfoNames {
		if name == candidate {
			index = i
			break
		}
	}
	if !visible || index < 0 || len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	var usage sqltype.Usage
	if index == 4 {
		if err := b.AdmitSetReturning(raw.Pos); err != nil {
			return nil, err
		}
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
			{Attribute: 1, Name: "rm_id", Usage: b.Type(sqltype.Int4).Usage()},
			{Attribute: 2, Name: "rm_name", Usage: b.Type(sqltype.Text).Usage()},
			{Attribute: 3, Name: "rm_builtin", Usage: b.Type(sqltype.Bool).Usage()},
		})
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
	} else {
		usage = b.Type(RecoveryInfoKind(index).result()).Usage()
	}
	info, err := b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, expr.Properties{Volatility: expr.Volatile}), CatalogRoutineProvider, name)
	if err != nil {
		return nil, err
	}
	if index == 4 {
		return WALResourceManagers{Info: info}, nil
	}
	return RecoveryInfo{Info: info, Kind: RecoveryInfoKind(index)}, nil
}

func (n RecoveryInfo) Validate() error {
	if n.Kind.Name() == "" || n.Usage.Type == nil || n.Usage.Type.ID() != n.Kind.result() {
		return fmt.Errorf("stuntgres: invalid recovery inquiry signature")
	}
	return nil
}

func (n WALResourceManagers) Validate() error {
	d := n.Usage.Type
	if d == nil || d.ID() != 2249 || !d.IsComposite() || d.CompositeLen() != 3 {
		return fmt.Errorf("stuntgres: invalid WAL resource manager record")
	}
	names := [...]string{"rm_id", "rm_name", "rm_builtin"}
	types := [...]sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Bool}
	for i := range names {
		m := d.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != names[i] || m.Usage.Type == nil || m.Usage.Type.ID() != types[i] {
			return fmt.Errorf("stuntgres: invalid WAL resource manager member")
		}
	}
	return nil
}

func (n RecoveryInfo) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	if n.Kind == RecoveryInProgress {
		return sqltype.Boolean(n.Usage.Type, false), nil
	}
	return sqltype.Null(n.Usage.Type), nil
}

func (n RecoveryInfo) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}

func (WALResourceManagers) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: WAL resource manager set used as scalar")
}

func (n WALResourceManagers) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return expr.SourceCursor{}, err
	}
	if err := n.Validate(); err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.SourceCursor{}, expr.ErrorAt("0A000", "pg_get_wal_resource_managers physical WAL resource manager registry is unsupported in embedded mode", parser.Position{})
}
