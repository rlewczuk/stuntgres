package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
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

func (b *expressionBinder) bindObjectACL(raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [3]comparisonOperand
	var small [3]string
	names := small[:0]
	if len(raw.Args) <= 3 {
		names = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		operand, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = operand
		}
		if i < len(names) {
			names[i] = "unknown"
			if typ := operand.descriptor(); typ != nil {
				names[i] = typ.DiagnosticName()
			}
			names[i] = functions.NamedArgumentType(raw, i, names[i])
		}
	}
	if _, err := b.bindAggregateFilter(parser.AggregateExpr{Filter: raw.Filter}); err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	issue := functions.FunctionLabels(raw)
	switch {
	case issue.Malformed:
		return nil, fmt.Errorf("invalid function argument labels")
	case issue.Positional:
		return nil, expr.ErrorAt("42601", "positional argument cannot follow named argument", issue.Position)
	case issue.Duplicate != "":
		return nil, expr.ErrorAt("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	if b.defaultConsumer {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	if !visible || raw.Name.Parts[len(raw.Name.Parts)-1].Text != "pg_get_acl" || len(raw.Args) != 3 {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
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
				return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
			}
			n.slots[i], n.labels[i] = uint8(slot), label
		}
		slot := n.slots[i]
		if occupied[slot] {
			return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
		}
		occupied[slot] = true
		if operands[i].descriptor() != nil {
			if _, failure := sqltype.PlanCoercion(operands[i].node.ResultUsage(), b.typ(objectACLTypes[slot]).Usage(), sqltype.Implicit); failure != nil {
				return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
			}
		}
	}
	properties := expr.Properties{Volatility: expr.Stable}
	for i := range n.args {
		target := b.typ(objectACLTypes[n.slots[i]]).Usage()
		arg, err := b.finalizeOperand(operands[i], target.Type)
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage() != target {
			plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
			if failure != nil {
				return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
			}
			arg = coercionExpression{b.info(target, raw.Args[i].SourcePosition(), coercionProperties(arg, plan)), arg, plan, sqltype.Implicit, false}
		}
		n.args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	switch {
	case raw.Distinct:
		return nil, expr.ErrorAt("42809", "DISTINCT specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expr.ErrorAt("42809", "ORDER BY specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expr.ErrorAt("42809", "FILTER specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
	}
	info, err := b.builtinRoutineInfo(b.info(b.typ(sqltype.ACLItemArray).Usage(), raw.Pos, properties), catalogRoutineProvider, "pg_get_acl", objectACLTypes[:]...)
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
	scope := c.liveScope()
	if scope == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: object ACL inquiry lacks statement authority")
	}
	e := scope.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := scope.writeAuthorizationLocked(e)
	if err != nil {
		return sqltype.Value{}, err
	}
	if scope.catalog != nil {
		auth.Recapture(e.privateCatalogLocked(scope.catalog))
	}
	acl, err := RawObjectACL(auth.Catalog, ids.ObjectAddress{ClassID: ids.OID(class), ObjectID: ids.OID(object), SubID: int32(subid)})
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.pgClassACL(auth.Roles, n.Usage.Type, acl)
}

// REL_18_0 ObjectProperty, not the native provider inventory. A catalog without
// native instances can still be a recognized address class. pg_largeobject is
// the released alias for pg_largeobject_metadata. pg_parameter_acl is not listed.
func ObjectACLClass(class ids.OID) (hasACL, known bool) {
	switch class {
	case 1259, 2615, 1247, 1255, 1262, 2612, 1213, 2328, 1417, 2995, 2613:
		return true, true
	case 2601, 2602, 2603, 2605, 3456, 2606, 2607, 826, 3079,
		2616, 2617, 2753, 1260, 1261, 2618, 3576, 2620, 3256, 3466,
		3602, 3600, 3601, 3764, 6104, 6100, 3381, 1418:
		return false, true
	}
	return false, false
}

func RawObjectACL(view catalog.Read, address ids.ObjectAddress) (authorization.ACLValue, error) {
	if address.ClassID == 0 && address.ObjectID == 0 {
		return authorization.ACLValue{}, nil
	}
	if address.ClassID == 2613 {
		address.ClassID = 2995
	}
	hasACL, known := ObjectACLClass(address.ClassID)
	if !known {
		return authorization.ACLValue{}, sqlError("XX000", fmt.Sprintf("unrecognized class ID: %d", address.ClassID))
	}
	if !hasACL {
		return authorization.ACLValue{}, nil
	}
	key := address
	key.SubID = 0
	d := view.Get(key)
	if address.ClassID == ids.RelationClassID && address.SubID != 0 {
		// AttrNumber is signed int16. Narrow only after choosing the column
		// path, so wrapped zero never becomes a whole-object ACL request.
		subid := int32(int16(address.SubID))
		if relation, ok := d.(*catalog.Relation); ok && subid > 0 && relation.HasAttribute(subid) {
			return relation.Security.ColumnACL[subid], nil
		}
		return authorization.ACLValue{}, nil
	}
	if typ, ok := d.(*catalog.Type); ok && typ.ACLSyntheticDefault {
		return authorization.ACLValue{}, nil
	}
	_, acl, _ := DescriptorACL(d)
	return acl, nil
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
