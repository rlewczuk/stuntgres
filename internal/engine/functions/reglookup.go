package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// RegLookupKind selects one released nullable registry-name signature.
type RegLookupKind uint8

const (
	RegLookupCollation RegLookupKind = iota
	RegLookupRole
	RegLookupProc
	RegLookupProcedure
	RegLookupOper
	RegLookupOperator
)

// RegLookup retains a typed TEXT operand and routine provenance, never a
// resolved object or the input environment of an earlier statement.
type RegLookup struct {
	expr.Info
	Arg  expr.Expr
	Kind RegLookupKind
}

func (n RegLookup) signature() (string, sqltype.ID, uint32) {
	switch n.Kind {
	case RegLookupCollation:
		return "to_regcollation", sqltype.Regcollation, 4195
	case RegLookupRole:
		return "to_regrole", sqltype.Regrole, 4093
	case RegLookupProc:
		return "to_regproc", sqltype.Regproc, 3494
	case RegLookupProcedure:
		return "to_regprocedure", sqltype.Regprocedure, 3479
	case RegLookupOper:
		return "to_regoper", sqltype.Regoper, 3492
	case RegLookupOperator:
		return "to_regoperator", sqltype.Regoperator, 3476
	default:
		return "", 0, 0
	}
}

func (n RegLookup) name() string {
	name, _, _ := n.signature()
	return name
}

func bindRegLookup(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	var inline [1]string
	names := inline[:0]
	if len(raw.Args) == 1 {
		names = inline[:]
	} else if len(raw.Args) <= 100 {
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
			name := "unknown"
			if arg.Usage.Type != nil {
				name = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	missing := func() error { return b.SignatureError(raw.Name, raw.Pos, names) }
	if !visible || len(raw.Args) != 1 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, missing()
	}
	var n RegLookup
	switch name {
	case "to_regcollation":
		n.Kind = RegLookupCollation
	case "to_regrole":
		n.Kind = RegLookupRole
	case "to_regproc":
		n.Kind = RegLookupProc
	case "to_regprocedure":
		n.Kind = RegLookupProcedure
	case "to_regoper":
		n.Kind = RegLookupOper
	case "to_regoperator":
		n.Kind = RegLookupOperator
	default:
		return nil, missing()
	}
	n.Arg, err = coerceInquiryFormal(b, operand, raw.Args[0], b.Type(sqltype.Text).Usage(), missing)
	if err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	_, result, _ := n.signature()
	usage := b.Type(result).Usage()
	if err := b.CaptureTypeInput(usage.Type); err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Stable}, n.Arg.Properties())
	n.Info, err = b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), CatalogRoutineProvider, name, sqltype.Text)
	if err != nil {
		return nil, err
	}
	return n, n.Validate()
}

func (n RegLookup) Validate() error {
	name, result, oid := n.signature()
	if name == "" || n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.ID() != sqltype.Text || n.Arg.ResultUsage() != n.Arg.ResultUsage().Type.Usage() {
		return fmt.Errorf("stuntgres: invalid registry lookup operand")
	}
	if n.Usage.Type == nil || n.Usage.Type.ID() != result || n.Usage != n.Usage.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid registry lookup result")
	}
	if n.RoutineReference == nil || n.RoutineReference.RoutineOID() != oid {
		return fmt.Errorf("stuntgres: invalid registry lookup routine")
	}
	if n.Property != expr.MergeProperties(expr.Properties{Volatility: expr.Stable}, n.Arg.Properties()) {
		return fmt.Errorf("stuntgres: invalid registry lookup properties")
	}
	return nil
}

func (n RegLookup) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.Arg.Eval(c)
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
	// Only the destination codec owns soft failures. Child, context and input
	// setup errors never pass through this recovery boundary.
	value, err = n.Usage.Type.InputWithEnvironment(ctx, text, -1, c.FunctionInput())
	if err != nil && sqltype.InputSoftError(err) {
		return sqltype.Null(n.Usage.Type), nil
	}
	return value, err
}
