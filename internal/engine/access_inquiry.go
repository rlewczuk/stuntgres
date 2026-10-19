package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Access inquiries retain only their typed recipe. All authority is borrowed
// from the executing statement, never cached on the expression or statement.
type accessInquiryExpression struct {
	expr.Info
	rowSecurity bool
	args        [3]expr.Expr
	count       int
}

func (n accessInquiryExpression) name() string {
	if n.rowSecurity {
		return "row_security_active"
	}
	return "pg_has_role"
}
func (n accessInquiryExpression) arguments() []expr.Expr { return n.args[:n.count] }

func bindAccessInquiry(b functions.Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [3]functions.Argument
	var small [3]string
	names := small[:0]
	if len(raw.Args) <= 3 {
		names = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		operand, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = operand
		}
		if i < len(names) {
			names[i] = "unknown"
			if typ := operand.Usage.Type; typ != nil {
				names[i] = typ.DiagnosticName()
			}
			names[i] = functions.NamedArgumentType(raw, i, names[i])
		}
	}
	if err := b.AnalyzeFilter(raw.Filter); err != nil {
		return nil, err
	}
	if err := inquiryCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	n := accessInquiryExpression{rowSecurity: name == "row_security_active", count: len(raw.Args)}
	if !visible || name != n.name() || len(raw.ArgNames) != 0 || n.rowSecurity && n.count != 1 || !n.rowSecurity && n.count != 2 && n.count != 3 {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	properties := expr.Properties{Volatility: expr.Stable}
	var formal [3]sqltype.ID
	for i := range n.count {
		id := sqltype.Text
		if n.rowSecurity || i < n.count-1 {
			id = sqltype.Name
			if n.rowSecurity {
				id = sqltype.Text
			}
			if operands[i].Usage.Type != nil {
				// The two overload axes are independent. Unknown strings/parameters
				// prefer NAME (TEXT for relations); typed numeric/alias inputs prefer OID.
				if _, failure := sqltype.PlanCoercion(operands[i].Usage, b.Type(id).Usage(), sqltype.Implicit); failure != nil {
					id = sqltype.Oid
				}
			}
		}
		formal[i] = id
		arg, err := coerceInquiryFormal(b, operands[i], raw.Args[i], b.Type(id).Usage(), func() error {
			return b.SignatureError(raw.Name, raw.Pos, names)
		})
		if err != nil {
			return nil, err
		}
		n.args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	// Arguments and FILTER are analyzed before scalar resolution. ORDER BY
	// children are not: a resolved scalar rejects that decoration first.
	// Matching scalar VARIADIC syntax is intentionally not rejected.
	if err := inquiryDecorationError(raw); err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), functions.CatalogRoutineProvider, name, formal[:n.count]...)
	if err != nil {
		return nil, err
	}
	n.Info = info
	return n, nil
}

// inquiryCallSyntax is the shared envelope of pg_get_acl and the access
// inquiries after argument and FILTER analysis: the argument limit, labels
// and DEFAULT admission.
func inquiryCallSyntax(b functions.Binder, raw parser.FunctionExpr) error {
	if len(raw.Args) > 100 {
		return expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	issue := functions.FunctionLabels(raw)
	switch {
	case issue.Malformed:
		return fmt.Errorf("invalid function argument labels")
	case issue.Positional:
		return expr.ErrorAt("42601", "positional argument cannot follow named argument", issue.Position)
	case issue.Duplicate != "":
		return expr.ErrorAt("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	if b.DefaultConsumer() {
		return expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	return nil
}

// inquiryDecorationError rejects aggregate decorations of a resolved scalar.
func inquiryDecorationError(raw parser.FunctionExpr) error {
	switch {
	case raw.Distinct:
		return expr.ErrorAt("42809", "DISTINCT specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return expr.ErrorAt("42809", "ORDER BY specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return expr.ErrorAt("42809", "FILTER specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
	}
	return nil
}

// coerceInquiryFormal finalizes an argument to its fixed formal and applies
// the implicit conversion at the raw argument; a failed plan is missing.
func coerceInquiryFormal(b functions.Binder, argument functions.Argument, raw parser.Expr, target sqltype.Usage, missing func() error) (expr.Expr, error) {
	node, err := b.CoerceArgument(argument, functions.CoercionRequest{Intent: functions.FinalizeArgument, Target: target})
	if err != nil || node.ResultUsage() == target {
		return node, err
	}
	plan, failure := sqltype.PlanCoercion(node.ResultUsage(), target, sqltype.Implicit)
	if failure != nil {
		return nil, missing()
	}
	return b.CoerceArgument(functions.Argument{Expression: node, Raw: raw, Usage: node.ResultUsage()}, functions.CoercionRequest{Intent: functions.BareFormalArgument, Target: target, Plan: plan, Planned: true})
}

func (n accessInquiryExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	null := false
	for i := range n.count {
		value, err := n.args[i].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	var result bool
	var err error
	if n.rowSecurity {
		relation := functions.AccessRelation{ByOID: n.args[0].ResultUsage().Type.ID() == sqltype.Oid}
		if relation.ByOID {
			relation.OID, err = values[0].OID()
		} else {
			relation.Name, err = values[0].Text()
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		result, err = c.RowSecurityActive(relation)
	} else {
		var actor *functions.AccessRole
		targetIndex := 0
		if n.count == 3 {
			role, failure := accessRoleValue(n.args[0], values[0])
			if failure != nil {
				return sqltype.Value{}, failure
			}
			actor, targetIndex = &role, 1
		}
		target, failure := accessRoleValue(n.args[targetIndex], values[targetIndex])
		if failure != nil {
			return sqltype.Value{}, failure
		}
		privilege, failure := values[n.count-1].Text()
		if failure != nil {
			return sqltype.Value{}, failure
		}
		result, err = c.RoleMembership(actor, target, privilege)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Boolean(n.Usage.Type, result), nil
}

func accessRoleValue(arg expr.Expr, value sqltype.Value) (functions.AccessRole, error) {
	var role functions.AccessRole
	var err error
	if arg.ResultUsage().Type.ID() == sqltype.Oid {
		role.ByOID = true
		role.OID, err = value.OID()
	} else {
		role.Name, err = value.Text()
	}
	return role, err
}

func (n accessInquiryExpression) validateStored() error {
	if n.rowSecurity && n.count != 1 || !n.rowSecurity && n.count != 2 && n.count != 3 {
		return fmt.Errorf("stuntgres: invalid stored access inquiry arity")
	}
	properties := expr.Properties{Volatility: expr.Stable}
	for i, arg := range n.args {
		if i >= n.count {
			if arg != nil {
				return fmt.Errorf("stuntgres: invalid stored access inquiry tail")
			}
			continue
		}
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage() != arg.ResultUsage().Type.Usage() {
			return fmt.Errorf("stuntgres: invalid stored access inquiry argument")
		}
		id := arg.ResultUsage().Type.ID()
		valid := id == sqltype.Text
		if !n.rowSecurity && i < n.count-1 {
			valid = id == sqltype.Name || id == sqltype.Oid
		} else if n.rowSecurity {
			valid = id == sqltype.Text || id == sqltype.Oid
		}
		if !valid {
			return fmt.Errorf("stuntgres: invalid stored access inquiry signature")
		}
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if n.Usage.Type == nil || n.Usage != n.Usage.Type.Usage() || n.Usage.Type.ID() != sqltype.Bool || n.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored access inquiry result")
	}
	return nil
}
