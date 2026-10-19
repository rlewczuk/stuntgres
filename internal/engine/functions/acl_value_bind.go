package functions

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindACLValue binds acldefault, makeaclitem and aclexplode. Their chain
// branch sat before the shared gates and claimed the names case-insensitively
// (parser.ACLValueFunctionName); exact spelling is part of resolution here.
func bindACLValue(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := ACLDefault
	if strings.EqualFold(name, "makeaclitem") {
		kind = ACLMake
	}
	if strings.EqualFold(name, "aclexplode") {
		kind = ACLExplode
	}
	types := kind.types()
	var operands [4]Argument
	var small [4]string
	names := small[:0]
	if len(raw.Args) <= 4 {
		names = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		// The object-kind slot consumes internal CHAR input just like the
		// existing textual function roles. Keep general CHAR casts/storage
		// excluded; in particular, typed TEXT is not an implicit CHAR input.
		intent := StringOtherArgument
		if kind == ACLDefault && i == 0 {
			intent = StringTextArgument
		}
		o, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: intent})
		if err != nil {
			return nil, err
		}
		if i < 4 {
			operands[i] = o
		}
		if i < len(names) {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = b.DiagnosticTypeName(d)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	valid := visible && name == kind.Name() && !raw.ResolutionOnly && !raw.Variadic && len(raw.Args) == len(types)
	named := false
	for _, label := range raw.ArgNames {
		if label.Text != "" {
			named = true
			valid = valid && kind == ACLExplode && label.Text == "acl"
		}
	}
	if valid {
		for i, t := range types {
			if operands[i].Usage.Type != nil {
				if _, err := sqltype.PlanCoercion(operands[i].Usage, b.Type(t).Usage(), sqltype.Implicit); err != nil {
					valid = false
				}
			}
		}
	}
	if !valid {
		return nil, ResolutionError(raw.Name, raw.Pos, names, false)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	n := ACLValue{Kind: kind, Named: named}
	p := n.baseProperties()
	for i, t := range types {
		arg, err := b.CoerceArgument(operands[i], CoercionRequest{Intent: ConditionalArgument, Target: b.Type(t).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
		n.Args[i] = arg
		p = expr.MergeProperties(p, arg.Properties())
	}
	result := b.Type(sqltype.ACLItemArray).Usage()
	if kind == ACLMake {
		result = b.Type(sqltype.ACLItem).Usage()
	}
	if kind == ACLExplode {
		members := []sqltype.CompositeMember{{Attribute: 1, Name: "grantor", Usage: b.Type(sqltype.Oid).Usage()}, {Attribute: 2, Name: "grantee", Usage: b.Type(sqltype.Oid).Usage()}, {Attribute: 3, Name: "privilege_type", Usage: b.Type(sqltype.Text).Usage()}, {Attribute: 4, Name: "is_grantable", Usage: b.Type(sqltype.Bool).Usage()}}
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
		result = shape.Usage()
	}
	n.Info, err = b.BuiltinRoutineInfo(b.Info(result, raw.Pos, p), CatalogRoutineProvider, kind.Name(), types...)
	if err != nil {
		return nil, err
	}
	return n, nil
}

// BindACLContains is the fixed aclitem[] @> aclitem operator over analyzed
// operands; namespace is the explicit operator schema, if any.
func BindACLContains(b Binder, operands [2]Argument, namespace string, pos parser.Position) (expr.Expr, error) {
	n := ACLValue{Kind: ACLContains, Namespace: namespace}
	properties := n.baseProperties()
	for i, typ := range n.Kind.types() {
		arg, err := b.CoerceArgument(operands[i], CoercionRequest{Intent: ConditionalArgument, Target: b.Type(typ).Usage(), Label: "@>"})
		if err != nil {
			return nil, err
		}
		n.Args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	info, err := b.BuiltinOperatorRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), pos, properties), "@>", sqltype.ACLItemArray, sqltype.ACLItem)
	if err != nil {
		return nil, err
	}
	n.Info = info
	return n, nil
}
