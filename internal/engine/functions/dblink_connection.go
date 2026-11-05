package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type DblinkConnectionOperation uint8

const (
	DblinkConnect DblinkConnectionOperation = iota
	DblinkConnectU
	DblinkDisconnect
	DblinkConnections
)

func (op DblinkConnectionOperation) Name() string {
	switch op {
	case DblinkConnect:
		return "dblink_connect"
	case DblinkConnectU:
		return "dblink_connect_u"
	case DblinkDisconnect:
		return "dblink_disconnect"
	case DblinkConnections:
		return "dblink_get_connections"
	}
	return ""
}

// DblinkConnection retains an operation and at most two typed operands, never
// a provider, owner, connection, conninfo value or caller authority.
type DblinkConnection struct {
	expr.Info
	Operation DblinkConnectionOperation
	Args      [2]expr.Expr
	Count     int
}

func (n DblinkConnection) Signature() string {
	switch n.Count {
	case 0:
		return ""
	case 1:
		return "25"
	case 2:
		return "25,25"
	}
	return "invalid"
}
func (n DblinkConnection) ResultOID() uint32 {
	if n.Operation == DblinkConnections {
		return 1009
	}
	return 25
}
func bindDblinkConnection(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var small [2]Argument
	operands := small[:]
	if len(raw.Args) > len(small) {
		operands = make([]Argument, len(raw.Args))
	} else {
		operands = operands[:len(raw.Args)]
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
	var call DblinkConnection
	call.Count = len(operands)
	switch raw.Name.Parts[len(raw.Name.Parts)-1].Text {
	case "dblink_connect":
		call.Operation = DblinkConnect
	case "dblink_connect_u":
		call.Operation = DblinkConnectU
	case "dblink_disconnect":
		call.Operation = DblinkDisconnect
	case "dblink_get_connections":
		call.Operation = DblinkConnections
	default:
		return nil, fmt.Errorf("stuntgres: invalid dblink connection binding")
	}
	routine, err := b.InstalledRoutine(raw.Name, DblinkExtension, call.Signature())
	if err != nil {
		return nil, err
	}
	valid := routine != nil && !raw.ResolutionOnly && !raw.Variadic && !raw.Star && len(raw.ArgNames) == 0
	switch call.Operation {
	case DblinkConnect, DblinkConnectU:
		valid = valid && (call.Count == 1 || call.Count == 2)
	case DblinkDisconnect:
		valid = valid && call.Count <= 1
	case DblinkConnections:
		valid = valid && call.Count == 0
	}
	if valid {
		for _, arg := range operands {
			if arg.Usage.Type != nil {
				if _, err := sqltype.PlanCoercion(arg.Usage, b.Type(sqltype.Text).Usage(), sqltype.Implicit); err != nil {
					valid = false
					break
				}
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
	properties := expr.Properties{Volatility: expr.Volatile, StageDependent: true}
	for i, arg := range operands {
		call.Args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: DirectFormalArgument, Target: b.Type(sqltype.Text).Usage()})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, call.Args[i].Properties())
	}
	call.Info = b.Info(b.Type(sqltype.ID(call.ResultOID())).Usage(), raw.Pos, properties)
	return routine.Attach(call)
}
func (DblinkConnection) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: dblink connection requires installed invocation authority")
}
func (n DblinkConnection) Children() []expr.Expr {
	if n.Count < 0 || n.Count > len(n.Args) {
		return nil
	}
	return n.Args[:n.Count:n.Count]
}
func (n DblinkConnection) ValidateArguments(children []expr.Expr) error {
	valid := n.Count >= 0 && n.Count <= 2 && n.Operation <= DblinkConnections
	switch n.Operation {
	case DblinkConnect, DblinkConnectU:
		valid = valid && n.Count >= 1
	case DblinkDisconnect:
		valid = valid && n.Count <= 1
	case DblinkConnections:
		valid = valid && n.Count == 0
	}
	if !valid || len(children) != n.Count {
		return fmt.Errorf("stuntgres: invalid dblink connection arity")
	}
	for _, child := range children {
		if child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.BaseType().ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid dblink connection argument")
		}
	}
	return nil
}
func (n DblinkConnection) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := n.ValidateArguments(children); err != nil {
		return nil, err
	}
	n.Info = info
	copy(n.Args[:], children)
	return n, nil
}
func (n DblinkConnection) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (DblinkConnection) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (DblinkConnection) PlanningFold() expr.Fold              { return expr.FoldNever }
func (DblinkConnection) SourceAdmission() (bool, bool)        { return true, false }
func (n DblinkConnection) StrictNull() expr.Strict {
	if n.Operation == DblinkConnections {
		return expr.StrictNone
	}
	return expr.StrictAfterBare
}
func (DblinkConnection) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (DblinkConnection) LooksLikeFunction() bool       { return true }
func (n DblinkConnection) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Args: n.Children(), Count: n.Count}, kind == expr.CaptureFunction, nil
}
func (n DblinkConnection) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.DblinkConnection")
	s.JumbleText(n.Operation.Name())
	s.JumbleText(n.Signature())
}
