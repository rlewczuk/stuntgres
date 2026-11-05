package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BackupControlKind identifies a physical online backup boundary, not state.
type BackupControlKind uint8

const (
	BackupStart BackupControlKind = iota
	BackupStop
)

var backupControlNames = [...]string{"pg_backup_start", "pg_backup_stop"}
var backupStartLabels = [...]string{"label", "fast"}
var backupStartInputs = [...]sqltype.ID{sqltype.Text, sqltype.Bool}
var backupStopLabels = [...]string{"wait_for_archive"}
var backupStopInputs = [...]sqltype.ID{sqltype.Bool}
var backupStopFields = [...]string{"lsn", "labelfile", "spcmapfile"}
var backupStopTypes = [...]sqltype.ID{sqltype.PgLSN, sqltype.Text, sqltype.Text}

func (k BackupControlKind) Name() string {
	if k > BackupStop {
		return ""
	}
	return backupControlNames[k]
}
func (k BackupControlKind) formals() ([]string, []sqltype.ID, int) {
	if k == BackupStart {
		return backupStartLabels[:], backupStartInputs[:], 1
	}
	return backupStopLabels[:], backupStopInputs[:], 0
}
func backupControlKind(name string) BackupControlKind {
	for i, candidate := range backupControlNames {
		if name == candidate {
			return BackupControlKind(i)
		}
	}
	return 255
}

// BackupControl owns supplied children in written order and formal destinations.
// Omission denotes a declaration default; no physical or authorization state is kept.
type BackupControl struct {
	expr.Info
	Kind  BackupControlKind
	Arity uint8
	Args  [2]expr.Expr
	Slots [2]uint8
}

func namedBackupControl(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	kind := backupControlKind(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	labels, inputs, required := kind.formals()
	if kind.Name() == "" || !declaredTypes(declared, inputs) {
		return nil, nil, false
	}
	return fixedNamedInputs(b, raw, args, declared, labels, required)
}

func bindBackupControl(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	kind := backupControlKind(name)
	labels, inputs, required := kind.formals()
	slots, valid := namedSlots(raw, labels, required)
	if !visible || kind.Name() == "" || !valid || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	for i := range raw.Args {
		if operands[i].Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(operands[i].Usage, b.Type(inputs[slots[i]]).Usage(), sqltype.Implicit); failure != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
		}
	}
	node := BackupControl{Kind: kind, Arity: uint8(len(raw.Args))}
	properties := expr.Properties{Volatility: expr.Volatile}
	for i := range raw.Args {
		target := b.Type(inputs[slots[i]]).Usage()
		arg, err := b.CoerceArgument(operands[i], CoercionRequest{Intent: ExactFormalArgument, Target: target})
		if err != nil {
			return nil, err
		}
		node.Args[i], node.Slots[i] = arg, uint8(slots[i])
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	usage := b.Type(sqltype.PgLSN).Usage()
	if kind == BackupStop {
		members := make([]sqltype.CompositeMember, 3)
		for i := range members {
			members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: backupStopFields[i], Usage: b.Type(backupStopTypes[i]).Usage()}
		}
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
	}
	node.Info, err = b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), CatalogRoutineProvider, name, inputs...)
	if err != nil {
		return nil, err
	}
	if err := node.Validate(); err != nil {
		return nil, err
	}
	return node, nil
}

func (n BackupControl) Validate() error {
	if n.Kind.Name() == "" || n.Usage.Type == nil || !n.Usage.Type.Concrete() || n.Usage != n.Usage.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid backup control signature")
	}
	_, inputs, required := n.Kind.formals()
	if int(n.Arity) > len(inputs) {
		return fmt.Errorf("stuntgres: invalid backup control arity")
	}
	var seen [2]bool
	for i := range int(n.Arity) {
		slot := n.Slots[i]
		if int(slot) >= len(inputs) || seen[slot] || n.Args[i] == nil {
			return fmt.Errorf("stuntgres: invalid backup control operand mapping")
		}
		seen[slot] = true
		u := n.Args[i].ResultUsage()
		if u.Type == nil || !u.Type.Concrete() || u != u.Type.Usage() || u.Type.ID() != inputs[slot] {
			return fmt.Errorf("stuntgres: invalid backup control operand type")
		}
	}
	for i := range required {
		if !seen[i] {
			return fmt.Errorf("stuntgres: missing backup control operand")
		}
	}
	for i := int(n.Arity); i < 2; i++ {
		if n.Args[i] != nil || n.Slots[i] != 0 {
			return fmt.Errorf("stuntgres: unused backup control operand")
		}
	}
	t := n.Usage.Type
	if n.Kind == BackupStart {
		if t.ID() != sqltype.PgLSN {
			return fmt.Errorf("stuntgres: invalid backup start result")
		}
		return nil
	}
	if t.ID() != 2249 || !t.IsComposite() || t.CompositeLen() != 3 {
		return fmt.Errorf("stuntgres: invalid backup stop record")
	}
	for i := range 3 {
		m := t.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != backupStopFields[i] || m.Usage.Type == nil || !m.Usage.Type.Concrete() || m.Usage.Type.ID() != backupStopTypes[i] || m.Usage != m.Usage.Type.Usage() {
			return fmt.Errorf("stuntgres: invalid backup stop record field")
		}
	}
	return nil
}

func (n BackupControl) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	var label sqltype.Value
	null := false
	for i := range int(n.Arity) {
		value, err := n.Args[i].Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		null = null || value.IsNull()
		if n.Kind == BackupStart && n.Slots[i] == 0 {
			label = value
		}
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	if n.Kind == BackupStart {
		text, err := label.Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		if len(text) > 1024 {
			return sqltype.Value{}, sqlError("22023", "backup label too long (max 1024 bytes)")
		}
	}
	return sqltype.Value{}, sqlError("0A000", n.Kind.Name()+" physical online backup lifecycle is unsupported in embedded mode")
}
func (n BackupControl) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
