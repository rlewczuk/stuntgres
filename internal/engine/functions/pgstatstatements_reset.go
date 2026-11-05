package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var pgStatStatementsResetTypes = [...]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Int8, sqltype.Bool}
var pgStatStatementsResetNames = [...]string{"userid", "dbid", "queryid", "minmax_only"}

// PgStatStatementsReset owns canonical typed operands, not collector state or
// authorization. Order preserves written argument effects before default literals.
type PgStatStatementsReset struct {
	expr.Info
	Args         [4]expr.Expr
	Order        [4]uint8
	WrittenCount uint8
	Labels       [4]string // Written order; omitted defaults have no source operands.
}

func bindPgStatStatementsReset(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var inline [4]Argument
	operands := inline[:min(len(raw.Args), len(inline))]
	if len(raw.Args) > len(inline) {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	routine, err := b.InstalledRoutine(raw.Name, PgStatStatementsExtension, "26,26,20,16")
	if err != nil {
		return nil, err
	}
	slots, valid := namedSlots(raw, pgStatStatementsResetNames[:], 0)
	valid = valid && routine != nil && !raw.ResolutionOnly && !raw.Variadic
	if valid {
		for i, arg := range operands {
			if arg.Usage.Type != nil && !b.CanCoerce(arg.Usage, b.Type(pgStatStatementsResetTypes[slots[i]]).Usage(), sqltype.Implicit) {
				valid = false
				break
			}
		}
	}
	if !valid {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", "pg_stat_statements_reset(*) specified, but pg_stat_statements_reset is not an aggregate function", raw.Pos)
	}
	n := PgStatStatementsReset{WrittenCount: uint8(len(operands))}
	for i, operand := range operands {
		slot := slots[i]
		n.Order[i] = uint8(slot)
		if len(raw.ArgNames) != 0 {
			n.Labels[i] = raw.ArgNames[i].Text
		}
		n.Args[slot], err = b.CoerceArgument(operand, CoercionRequest{Intent: DirectFormalArgument, Target: b.Type(pgStatStatementsResetTypes[slot]).Usage()})
		if err != nil {
			return nil, err
		}
	}
	at := len(operands)
	for slot, arg := range n.Args {
		if arg != nil {
			continue
		}
		var value sqltype.Value
		switch slot {
		case 0, 1:
			value, err = sqltype.OIDValue(b.Type(sqltype.Oid), 0)
		case 2:
			value, err = sqltype.Integer(b.Type(sqltype.Int8), 0)
		case 3:
			value = sqltype.Boolean(b.Type(sqltype.Bool), false)
		}
		if err != nil {
			return nil, err
		}
		n.Args[slot] = b.Literal(value, parser.Position{})
		n.Order[at] = uint8(slot)
		at++
	}
	properties := expr.Properties{Volatility: expr.Volatile, StageDependent: true}
	for _, arg := range n.Args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	n.Info = b.Info(b.Type(sqltype.Timestamptz).Usage(), raw.Pos, properties)
	return routine.Attach(n)
}

func namedPgStatStatementsReset(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Variadic || !declaredTypes(declared, pgStatStatementsResetTypes[:]) {
		return nil, nil, false
	}
	return fixedNamedInputs(b, raw, args, declared, pgStatStatementsResetNames[:], 0)
}

func (n PgStatStatementsReset) Eval(ctx expr.Context) (sqltype.Value, error) {
	var values [4]sqltype.Value
	for _, slot := range n.Order {
		value, err := n.Args[slot].Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[slot] = value
	}
	for _, value := range values {
		if value.IsNull() {
			return sqltype.Null(n.Usage.Type), nil
		}
	}
	body, err := n.EnterRoutine(ctx, values[:])
	if err != nil {
		return sqltype.Value{}, err
	}
	defer body.Leave()
	user, err := values[0].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	database, err := values[1].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	query, err := values[2].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	minmax, err := values[3].Boolean()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, ok := ctx.(interface {
		PgStatStatementsReset(uint32, uint32, int64, bool, *sqltype.Descriptor) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing statement statistics reset runtime")
	}
	return runtime.PgStatStatementsReset(user, database, query, minmax, n.Usage.Type)
}

func (n PgStatStatementsReset) Children() []expr.Expr { return n.Args[:] }
func (n PgStatStatementsReset) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(n.Args) || n.WrittenCount > 4 || info.Usage.Type == nil || info.Usage.Type.ID() != sqltype.Timestamptz {
		return nil, fmt.Errorf("stuntgres: invalid statement statistics reset shape")
	}
	var seen [4]bool
	for i, child := range children {
		if child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.ID() != pgStatStatementsResetTypes[i] || n.Order[i] >= 4 || seen[n.Order[i]] {
			return nil, fmt.Errorf("stuntgres: invalid statement statistics reset argument")
		}
		seen[n.Order[i]] = true
		n.Args[i] = child
	}
	named := false
	for i, slot := range n.Order {
		label := n.Labels[i]
		if i >= int(n.WrittenCount) {
			if label != "" {
				return nil, fmt.Errorf("stuntgres: default reset operand has a source label")
			}
		} else if label != "" {
			if label != pgStatStatementsResetNames[slot] {
				return nil, fmt.Errorf("stuntgres: invalid reset argument label")
			}
			named = true
		} else if named || slot != uint8(i) {
			return nil, fmt.Errorf("stuntgres: invalid positional reset argument")
		}
	}
	n.Info = info
	return n, nil
}
func (n PgStatStatementsReset) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (PgStatStatementsReset) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (PgStatStatementsReset) PlanningFold() expr.Fold              { return expr.FoldNever }
func (PgStatStatementsReset) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (PgStatStatementsReset) LooksLikeFunction() bool              { return true }
func (n PgStatStatementsReset) WrittenArguments(raw parser.FunctionExpr) ([]expr.Expr, error) {
	slots, valid := namedSlots(raw, pgStatStatementsResetNames[:], 0)
	if !valid || len(raw.Args) != int(n.WrittenCount) {
		return nil, fmt.Errorf("stuntgres: invalid statement statistics reset syntax capture")
	}
	args := make([]expr.Expr, len(raw.Args))
	for i := range args {
		if uint8(slots[i]) != n.Order[i] {
			return nil, fmt.Errorf("stuntgres: mismatched reset written argument")
		}
		args[i] = n.Args[slots[i]]
	}
	return args, nil
}

func (PgStatStatementsReset) SourceAdmission() (bool, bool) { return true, false }
func (PgStatStatementsReset) StrictNull() expr.Strict       { return expr.StrictAfter }

// Released FuncExpr jumbling precedes default insertion. Keep executable
// defaults out of source identity, and retain named arguments' formal slots.
func (n PgStatStatementsReset) JumbleChildren(visit func(expr.Expr), fields expr.StatementFields) {
	fields.JumbleUint64(uint64(n.WrittenCount))
	for i := range n.WrittenCount {
		slot := n.Order[i]
		if n.Labels[i] != "" {
			fields.JumbleText("NamedArgExpr")
			fields.JumbleUint64(uint64(slot))
		}
		visit(n.Args[slot])
	}
}
