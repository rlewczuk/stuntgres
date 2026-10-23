package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindObjectACL binds pg_get_acl(classid, objid, objsubid), with optional
// named notation. Its chain branch sat before the shared gates and claimed
// the name case-insensitively; exact spelling is part of resolution here.
func bindObjectACL(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	if !visible || raw.Name.Parts[len(raw.Name.Parts)-1].Text != "pg_get_acl" || len(raw.Args) != 3 {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	n := ObjectACL{Slots: [3]uint8{0, 1, 2}}
	mapped, valid := namedSlots(raw, objectACLLabels[:], 3)
	if !valid {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	for i := range n.Args {
		slot := mapped[i]
		n.Slots[i] = uint8(slot)
		if len(raw.ArgNames) > i {
			n.Labels[i] = raw.ArgNames[i].Text
		}
		if operands[i].Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(operands[i].Usage, b.Type(objectACLTypes[slot]).Usage(), sqltype.Implicit); failure != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
		}
	}
	properties := expr.Properties{Volatility: expr.Stable}
	for i := range n.Args {
		arg, err := coerceInquiryFormal(b, operands[i], raw.Args[i], b.Type(objectACLTypes[n.Slots[i]]).Usage(), func() error {
			return b.SignatureError(raw.Name, raw.Pos, names)
		})
		if err != nil {
			return nil, err
		}
		n.Args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if err := inquiryDecorationError(raw); err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.ACLItemArray).Usage(), raw.Pos, properties), CatalogRoutineProvider, "pg_get_acl", objectACLTypes[:]...)
	if err != nil {
		return nil, err
	}
	n.Info = info
	return n, nil
}

func namedObjectACL(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if len(args) != 3 || !declaredTypes(declared, objectACLTypes[:]) {
		return nil, nil, false
	}
	return fixedNamedInputs(b, raw, args, declared, objectACLLabels[:], 3)
}
