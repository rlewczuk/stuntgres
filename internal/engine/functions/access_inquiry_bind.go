package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindAccessInquiry shares exact spelling, scalar gates and independent
// name/OID axes across the typed access inquiry families.
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
	n := AccessInquiry{Count: len(raw.Args)}
	switch name {
	case "row_security_active":
		n.Kind = AccessRowSecurity
	case "has_function_privilege":
		n.Kind = AccessFunction
	case "has_language_privilege":
		n.Kind = AccessLanguage
	case "has_foreign_data_wrapper_privilege":
		n.Kind = AccessForeignWrapper
	case "has_server_privilege":
		n.Kind = AccessServer
	case "has_database_privilege":
		n.Kind = AccessDatabase
	case "has_schema_privilege":
		n.Kind = AccessSchema
	case "has_type_privilege":
		n.Kind = AccessType
	case "has_tablespace_privilege":
		n.Kind = AccessTablespace
	}
	if !visible || name != n.Name() || len(raw.ArgNames) != 0 || n.Kind == AccessRowSecurity && n.Count != 1 || n.Kind != AccessRowSecurity && n.Count != 2 && n.Count != 3 {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	properties := expr.Properties{Volatility: expr.Stable}
	var formal [3]sqltype.ID
	for i := range n.Count {
		id := sqltype.Text
		if n.Kind == AccessRowSecurity || i < n.Count-1 {
			id = sqltype.Name
			if n.Kind == AccessRowSecurity || n.Kind >= AccessFunction && i == n.Count-2 {
				id = sqltype.Text
			}
			if operands[i].Usage.Type != nil {
				// Unknown role inputs prefer NAME; object identities prefer TEXT.
				// Each typed numeric/alias axis independently prefers OID.
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
