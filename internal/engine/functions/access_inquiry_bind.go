package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindAccessInquiry shares exact spelling, scalar gates and independent
// name/OID axes across the typed access inquiry families.
func bindAccessInquiry(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [4]Argument
	var small [4]string
	names := small[:0]
	if len(raw.Args) <= 4 {
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
	case "has_table_privilege":
		n.Kind = AccessTable
	case "has_sequence_privilege":
		n.Kind = AccessSequence
	case "has_column_privilege":
		n.Kind = AccessColumnPrivilege
	case "has_any_column_privilege":
		n.Kind = AccessAnyColumn
	}
	if !visible || name != n.Name() || len(raw.ArgNames) != 0 || !n.validArity() {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	properties := expr.Properties{Volatility: expr.Stable}
	var formal [4]sqltype.ID
	for i := range n.Count {
		id, alternate := n.formalAxis(i)
		if alternate != 0 && operands[i].Usage.Type != nil {
			// Unknown axes prefer the released textual formal.
			if _, failure := sqltype.PlanCoercion(operands[i].Usage, b.Type(id).Usage(), sqltype.Implicit); failure != nil {
				id = alternate
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
