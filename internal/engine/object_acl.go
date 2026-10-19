package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Arguments remain in written order. slots maps them to the fixed SQL signature;
// both evaluation and stored rendering retain that order, including named calls.
// No result, role read or catalog snapshot belongs to this immutable recipe.
type objectACLExpression struct {
	expr.Info
	args   [3]expr.Expr
	slots  [3]uint8
	labels [3]string
}

var objectACLLabels = [3]string{"classid", "objid", "objsubid"}
var objectACLTypes = [3]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Int4}

func bindObjectACL(b functions.Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	if !visible || raw.Name.Parts[len(raw.Name.Parts)-1].Text != "pg_get_acl" || len(raw.Args) != 3 {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	n := objectACLExpression{slots: [3]uint8{0, 1, 2}}
	var occupied [3]bool
	for i := range n.args {
		if len(raw.ArgNames) != 0 && raw.ArgNames[i].Text != "" {
			label := raw.ArgNames[i].Text
			slot := -1
			for j, formal := range objectACLLabels {
				if label == formal {
					slot = j
					break
				}
			}
			if slot < 0 {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
			n.slots[i], n.labels[i] = uint8(slot), label
		}
		slot := n.slots[i]
		if occupied[slot] {
			return nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
		occupied[slot] = true
		if operands[i].Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(operands[i].Usage, b.Type(objectACLTypes[slot]).Usage(), sqltype.Implicit); failure != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
		}
	}
	properties := expr.Properties{Volatility: expr.Stable}
	for i := range n.args {
		arg, err := coerceInquiryFormal(b, operands[i], raw.Args[i], b.Type(objectACLTypes[n.slots[i]]).Usage(), func() error {
			return b.SignatureError(raw.Name, raw.Pos, names)
		})
		if err != nil {
			return nil, err
		}
		n.args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if err := inquiryDecorationError(raw); err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.ACLItemArray).Usage(), raw.Pos, properties), functions.CatalogRoutineProvider, "pg_get_acl", objectACLTypes[:]...)
	if err != nil {
		return nil, err
	}
	n.Info = info
	return n, nil
}

func (n objectACLExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	null := false
	for i, arg := range n.args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[n.slots[i]] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	class, err := values[0].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	object, err := values[1].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	subid, err := values[2].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	return c.ObjectACL(ids.ObjectAddress{ClassID: ids.OID(class), ObjectID: ids.OID(object), SubID: int32(subid)}, n.Usage.Type)
}

func (n objectACLExpression) validateStored() error {
	var seen [3]bool
	properties := expr.Properties{Volatility: expr.Stable}
	named := false
	for i, arg := range n.args {
		slot := n.slots[i]
		if slot >= 3 || seen[slot] || arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage() != arg.ResultUsage().Type.Usage() || arg.ResultUsage().Type.ID() != objectACLTypes[slot] {
			return fmt.Errorf("stuntgres: invalid stored object ACL signature")
		}
		seen[slot] = true
		if n.labels[i] != "" {
			if n.labels[i] != objectACLLabels[slot] {
				return fmt.Errorf("stuntgres: invalid stored object ACL label")
			}
			named = true
		} else if named || int(slot) != i {
			return fmt.Errorf("stuntgres: invalid stored object ACL argument order")
		}
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if n.Usage.Type == nil || n.Usage != n.Usage.Type.Usage() || n.Usage.Type.ID() != sqltype.ACLItemArray || n.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored object ACL result")
	}
	return nil
}

func (n objectACLExpression) sql(name string, render func(expr.Expr) (string, error)) (string, error) {
	if err := n.validateStored(); err != nil {
		return "", err
	}
	var parts [3]string
	for i, arg := range n.args {
		text, err := render(arg)
		if err != nil {
			return "", err
		}
		if n.labels[i] != "" {
			text = n.labels[i] + " => " + text
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts[:], ", ") + ")", nil
}
