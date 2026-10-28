package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var snapshotNames = [...]string{"pg_current_snapshot", "pg_snapshot_xmin", "pg_snapshot_xmax", "pg_snapshot_xip", "pg_visible_in_snapshot"}
var legacySnapshotNames = [...]string{"txid_current_snapshot", "txid_snapshot_xmin", "txid_snapshot_xmax", "txid_snapshot_xip", "txid_visible_in_snapshot"}

const (
	snapshotCurrent = iota
	snapshotMin
	snapshotMax
	snapshotXip
	snapshotVisible
)

type snapshotExpression struct {
	expr.Info
	kind   int
	legacy bool
	args   []expr.Expr
}

func snapshotTypes(legacy bool) (sqltype.ID, sqltype.ID) {
	if legacy {
		return sqltype.Int8, sqltype.TxidSnapshot
	}
	return sqltype.Xid8, sqltype.PgSnapshot
}
func snapshotFormals(kind int, legacy bool) []sqltype.ID {
	id, snapshot := snapshotTypes(legacy)
	switch kind {
	case snapshotCurrent:
		return nil
	case snapshotVisible:
		return []sqltype.ID{id, snapshot}
	}
	return []sqltype.ID{snapshot}
}
func (n snapshotExpression) name() string {
	if n.legacy {
		return legacySnapshotNames[n.kind]
	}
	return snapshotNames[n.kind]
}
func bindSnapshot(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := -1
	legacy := false
	for i, n := range snapshotNames {
		if n == name {
			kind = i
			break
		}
	}
	if kind < 0 {
		for i, n := range legacySnapshotNames {
			if n == name {
				kind, legacy = i, true
				break
			}
		}
	}
	operands := make([]Argument, len(raw.Args))
	names := make([]string, len(raw.Args))
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = arg
		names[i] = "unknown"
		if arg.Usage.Type != nil {
			names[i] = b.DiagnosticTypeName(arg.Usage.Type)
		}
		names[i] = NamedArgumentType(raw, i, names[i])
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if kind < 0 || !visible || raw.ResolutionOnly || raw.Variadic || len(raw.ArgNames) != 0 {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	formals := snapshotFormals(kind, legacy)
	valid := len(operands) == len(formals)
	if valid {
		for i, arg := range operands {
			if arg.Usage.Type != nil {
				if legacy && formals[i] == sqltype.Int8 {
					if _, err := sqltype.PlanCoercion(arg.Usage, b.Type(sqltype.Int8).Usage(), sqltype.Implicit); err != nil {
						valid = false
					}
				} else if arg.Usage.Type.BaseType() != b.Type(formals[i]) {
					valid = false
				}
			}
		}
	}
	if !valid {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	args := make([]expr.Expr, len(operands))
	properties := expr.Properties{Volatility: expr.Immutable}
	if kind == snapshotCurrent {
		properties.Volatility = expr.Stable
	}
	for i, arg := range operands {
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(formals[i]).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	if kind == snapshotXip {
		if err := b.AdmitSetReturning(raw.Pos); err != nil {
			return nil, err
		}
		properties.StageDependent = true
	}
	result, snapshot := snapshotTypes(legacy)
	if kind == snapshotCurrent {
		result = snapshot
	} else if kind == snapshotVisible {
		result = sqltype.Bool
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, formals...)
	if err != nil {
		return nil, err
	}
	return snapshotExpression{Info: info, kind: kind, legacy: legacy, args: args}, nil
}
func (n snapshotExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if n.kind == snapshotCurrent {
		runtime, err := transactionInquiryRuntime(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if err = runtime.CheckTransactionInquiryContext(); err != nil {
			return sqltype.Value{}, err
		}
		snapshot, err := runtime.CurrentSnapshot()
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.SnapshotValue(n.Usage.Type, snapshot)
	}
	var values [2]sqltype.Value
	null := false
	for i, arg := range n.args {
		value, err := arg.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	index := 0
	if n.kind == snapshotVisible {
		index = 1
	}
	snapshot, err := values[index].Snapshot()
	if err != nil {
		return sqltype.Value{}, err
	}
	switch n.kind {
	case snapshotMin:
		return snapshotIDValue(n.Usage.Type, snapshot.Xmin())
	case snapshotMax:
		return snapshotIDValue(n.Usage.Type, snapshot.Xmax())
	case snapshotVisible:
		id, err := snapshotID(values[0])
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Boolean(n.Usage.Type, snapshot.Visible(id)), nil
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: snapshot set requires an invocation")
}
func (n snapshotExpression) SourceAdmission() (bool, bool) { return true, n.kind == snapshotXip }
func (n snapshotExpression) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if n.kind != snapshotXip {
		value, err := n.Eval(ctx)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(value), nil
	}
	value, err := n.args[0].Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, Value: value, Advance: nextSnapshotMember}), nil
}
func nextSnapshotMember(_ context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if cursor.Value.IsNull() {
		return sqltype.Value{}, false, nil
	}
	snapshot, err := cursor.Value.Snapshot()
	if err != nil {
		return sqltype.Value{}, false, err
	}
	if cursor.Position == snapshot.Len() {
		return sqltype.Value{}, false, nil
	}
	value, err := snapshotIDValue(cursor.Type, snapshot.Member(cursor.Position))
	cursor.Position++
	return value, true, err
}

func snapshotIDValue(d *sqltype.Descriptor, id uint64) (sqltype.Value, error) {
	if d.ID() == sqltype.Int8 {
		return sqltype.Integer(d, int64(id))
	}
	return sqltype.XID8Value(d, id)
}
func snapshotID(value sqltype.Value) (uint64, error) {
	if value.Type().BaseType().ID() == sqltype.Int8 {
		id, err := value.Integer()
		return uint64(id), err
	}
	return value.XID8()
}
