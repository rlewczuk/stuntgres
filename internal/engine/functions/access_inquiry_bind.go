package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindAccessInquiry binds pg_has_role and row_security_active. Their chain
// branch sat before the shared gates and claimed the names
// case-insensitively (parser.AccessInquiryFunctionName); exact spelling is
// part of resolution here.
func bindAccessInquiry(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [3]Argument
	var small [3]string
	names := small[:0]
	if len(raw.Args) <= 3 {
		names = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		operand, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
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
			names[i] = NamedArgumentType(raw, i, names[i])
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
	n := AccessInquiry{RowSecurity: name == "row_security_active", Count: len(raw.Args)}
	if !visible || name != n.Name() || len(raw.ArgNames) != 0 || n.RowSecurity && n.Count != 1 || !n.RowSecurity && n.Count != 2 && n.Count != 3 {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	properties := expr.Properties{Volatility: expr.Stable}
	var formal [3]sqltype.ID
	for i := range n.Count {
		id := sqltype.Text
		if n.RowSecurity || i < n.Count-1 {
			id = sqltype.Name
			if n.RowSecurity {
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
		n.Args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	// Arguments and FILTER are analyzed before scalar resolution. ORDER BY
	// children are not: a resolved scalar rejects that decoration first.
	// Matching scalar VARIADIC syntax is intentionally not rejected.
	if err := inquiryDecorationError(raw); err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, formal[:n.Count]...)
	if err != nil {
		return nil, err
	}
	n.Info = info
	return n, nil
}
