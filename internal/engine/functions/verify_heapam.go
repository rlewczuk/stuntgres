package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var verifyHeapamTypes = [...]sqltype.ID{sqltype.Regclass, sqltype.Bool, sqltype.Bool, sqltype.Text, sqltype.Int8, sqltype.Int8}
var verifyHeapamNames = [...]string{"relation", "on_error_stop", "check_toast", "skip", "startblock", "endblock"}

// VerifyHeapam retains canonical inputs only, with invocation-private runtime
// state. The installed wrapper supplies exact procedure and current ACL authority.
type VerifyHeapam struct {
	expr.Info
	Args [6]expr.Expr
}

func bindVerifyHeapam(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var small [6]Argument
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) { operands = make([]Argument, len(raw.Args)) }
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil { return nil, err }
	routine, err := b.InstalledRoutine(raw.Name, AmcheckExtension, "2205,16,16,25,20,20")
	if err != nil { return nil, err }
	slots, valid := namedSlots(raw, verifyHeapamNames[:], 1)
	valid = valid && routine != nil && !raw.ResolutionOnly && !raw.Variadic
	if valid {
		for i, arg := range operands {
			if arg.Usage.Type != nil && !b.CanCoerce(arg.Usage, b.Type(verifyHeapamTypes[slots[i]]).Usage(), sqltype.Implicit) { valid = false; break }
		}
	}
	if !valid { return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands)) }
	if err := routine.Validate(); err != nil { return nil, err }
	if err := CallDecorationError(raw); err != nil { return nil, err }
	if raw.Star { return nil, expr.ErrorAt("42809", "verify_heapam(*) specified, but verify_heapam is not an aggregate function", raw.Pos) }
	var args [6]expr.Expr
	for i, arg := range operands {
		slot := slots[i]
		args[slot], err = b.CoerceArgument(arg, CoercionRequest{Intent: DirectFormalArgument, Target: b.Type(verifyHeapamTypes[slot]).Usage()})
		if err != nil { return nil, err }
	}
	for slot, arg := range args {
		if arg != nil { continue }
		var value sqltype.Value
		switch slot {
		case 1, 2: value = sqltype.Boolean(b.Type(sqltype.Bool), false)
		case 3: value = sqltype.String(b.Type(sqltype.Text), "none")
		case 4, 5: value = sqltype.Null(b.Type(sqltype.Int8))
		default: return nil, fmt.Errorf("stuntgres: missing verify_heapam relation")
		}
		args[slot] = b.Literal(value, raw.Pos)
	}
	members := make([]sqltype.CompositeMember, 4)
	for i, name := range [...]string{"blkno", "offnum", "attnum", "msg"} {
		members[i] = sqltype.CompositeMember{Attribute: int32(i+1), Name: name, Usage: b.Type(([...]sqltype.ID{sqltype.Int8, sqltype.Int4, sqltype.Int4, sqltype.Text})[i]).Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil { return nil, err }
	properties := expr.Properties{Volatility: expr.Volatile, StageDependent: true}
	for _, arg := range args { properties = expr.MergeProperties(properties, arg.Properties()) }
	return routine.Attach(VerifyHeapam{Info: b.Info(shape.Usage(), raw.Pos, properties), Args: args})
}
func namedVerifyHeapam(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Variadic || !declaredTypes(declared, verifyHeapamTypes[:]) { return nil, nil, false }
	return fixedNamedInputs(b, raw, args, declared, verifyHeapamNames[:], 1)
}
func (n VerifyHeapam) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: verify_heapam requires a source invocation")
}
func (n VerifyHeapam) Children() []expr.Expr { return n.Args[:] }
func (n VerifyHeapam) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 6 { return nil, fmt.Errorf("stuntgres: invalid verify_heapam arguments") }
	for i, child := range children {
		if child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.BaseType().ID() != verifyHeapamTypes[i] { return nil, fmt.Errorf("stuntgres: invalid verify_heapam argument") }
		n.Args[i] = child
	}
	n.Info = info
	return n, nil
}
func (n VerifyHeapam) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) { return n.Rebuild(info, children) }
func (VerifyHeapam) SourceAdmission() (bool, bool) { return true, true }
type VerifyHeapamRuntime interface { VerifyHeapam([6]sqltype.Value, *sqltype.Descriptor) ([]sqltype.Value, error) }
func (n VerifyHeapam) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	var values [6]sqltype.Value
	for i, arg := range n.Args {
		value, err := arg.Eval(ctx)
		if err != nil { return expr.SourceCursor{}, err }
		values[i] = value
	}
	runtime, ok := ctx.(VerifyHeapamRuntime)
	if !ok { return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing verify_heapam statement runtime") }
	rows, err := runtime.VerifyHeapam(values, n.Usage.Type)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.OwnedSource(n.Usage.Type, rows), nil
}
func (VerifyHeapam) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (VerifyHeapam) PlanningFold() expr.Fold { return expr.FoldNever }
func (VerifyHeapam) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (VerifyHeapam) LooksLikeFunction() bool { return true }

// WrittenArguments maps source syntax back to canonical children for the shared
// stored-literal capture protocol; omitted defaults have no source occurrence.
func (n VerifyHeapam) WrittenArguments(raw parser.FunctionExpr) ([]expr.Expr, error) {
	slots, valid := namedSlots(raw, verifyHeapamNames[:], 1)
	if !valid { return nil, fmt.Errorf("stuntgres: invalid verify_heapam syntax capture") }
	args := make([]expr.Expr, len(raw.Args))
	for i := range args { args[i] = n.Args[slots[i]] }
	return args, nil
}
