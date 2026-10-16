package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type objectAddressOperation uint8

const (
	identifyObject objectAddressOperation = iota
	identifyObjectAddress
	getObjectAddress
	describeObject
)

func (op objectAddressOperation) name() string {
	switch op {
	case identifyObject:
		return "pg_identify_object"
	case identifyObjectAddress:
		return "pg_identify_object_as_address"
	case getObjectAddress:
		return "pg_get_object_address"
	case describeObject:
		return "pg_describe_object"
	}
	return ""
}
func (op objectAddressOperation) signature() ([3]sqltype.ID, [3]string) {
	if op == getObjectAddress {
		return [3]sqltype.ID{sqltype.Text, 1009, 1009}, [3]string{"type", "object_names", "object_args"}
	}
	labels := [3]string{"classid", "objid", "objsubid"}
	if op == describeObject {
		labels = [3]string{}
	}
	return [3]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Int4}, labels
}

var objectAddressRecords = [...]struct {
	names []string
	types []sqltype.ID
}{
	{[]string{"type", "schema", "name", "identity"}, []sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Text, sqltype.Text}},
	{[]string{"type", "object_names", "object_args"}, []sqltype.ID{sqltype.Text, 1009, 1009}},
	{[]string{"classid", "objid", "objsubid"}, []sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Int4}},
}

func (op objectAddressOperation) fields() ([]string, []sqltype.ID) {
	if op >= describeObject {
		return nil, nil
	}
	return objectAddressRecords[op].names, objectAddressRecords[op].types
}

// Arguments, labels and coercions retain written order independently of slots.
// The immutable recipe never retains a mutable catalog or a computed result.
type objectAddressExpression struct {
	expressionInfo
	op     objectAddressOperation
	args   [3]BoundExpr
	slots  [3]uint8
	labels [3]string
}

func (b *expressionBinder) bindObjectAddress(raw parser.FunctionExpr) (BoundExpr, error) {
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
		if i < 3 {
			operands[i] = operand
		}
		if i < len(names) {
			name := "unknown"
			if typ := operand.descriptor(); typ != nil {
				name = typ.DiagnosticName()
			}
			names[i] = functionNamedType(raw, i, name)
		}
	}
	if err := b.validateRangeInspectSyntax(raw); err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	op := identifyObject
	for op <= describeObject && op.name() != name {
		op++
	}
	if !visible || op > describeObject || len(raw.Args) != 3 || raw.ResolutionOnly {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	ids, labels := op.signature()
	n := objectAddressExpression{op: op, slots: [3]uint8{0, 1, 2}}
	var occupied [3]bool
	for i := range n.args {
		if len(raw.ArgNames) != 0 && raw.ArgNames[i].Text != "" {
			label := raw.ArgNames[i].Text
			slot := -1
			for j, formal := range labels {
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
			if _, failure := sqltype.PlanCoercion(operands[i].node.ResultUsage(), b.typ(ids[slot]).Usage(), sqltype.Implicit); failure != nil {
				return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
			}
		}
	}
	properties := ExprProperties{Volatility: Stable}
	for i := range n.args {
		target := b.typ(ids[n.slots[i]]).Usage()
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
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	if err := rangeInspectDecorationError(raw); err != nil {
		return nil, err
	}
	usage := b.typ(sqltype.Text).Usage()
	if op != describeObject {
		labels, ids := op.fields()
		members := make([]sqltype.CompositeMember, len(labels))
		for i, name := range labels {
			members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: b.typ(ids[i]).Usage()}
		}
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
	}
	n.expressionInfo, err = b.builtinRoutineInfo(b.info(usage, raw.Pos, properties), catalogRoutineProvider, op.name(), ids[:]...)
	if err != nil {
		return nil, err
	}
	return n, nil
}
func (n objectAddressExpression) validateStored() error {
	if n.op > describeObject || !n.usage.Valid() {
		return fmt.Errorf("stuntgres: invalid object address operation")
	}
	ids, labels := n.op.signature()
	var seen [3]bool
	named := false
	properties := ExprProperties{Volatility: Stable}
	for i, arg := range n.args {
		slot := n.slots[i]
		if slot >= 3 || seen[slot] || arg == nil || !arg.ResultUsage().Valid() || arg.ResultUsage() != arg.ResultUsage().Type.Usage() || arg.ResultUsage().Type.ID() != ids[slot] {
			return fmt.Errorf("stuntgres: invalid object address signature")
		}
		seen[slot] = true
		if n.labels[i] != "" {
			if n.labels[i] != labels[slot] {
				return fmt.Errorf("stuntgres: invalid object address label")
			}
			named = true
		} else if named || int(slot) != i {
			return fmt.Errorf("stuntgres: invalid object address argument order")
		}
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	if properties != n.Properties() {
		return fmt.Errorf("stuntgres: invalid object address properties")
	}
	if n.op == describeObject {
		if n.usage.Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid object description result")
		}
		return nil
	}
	outLabels, types := n.op.fields()
	shape := n.usage.Type
	if !shape.IsComposite() || shape.CompositeLen() != len(outLabels) {
		return fmt.Errorf("stuntgres: invalid object address record")
	}
	for i, label := range outLabels {
		member := shape.CompositeMember(i)
		if member.Name != label || member.Attribute != int32(i+1) || member.Usage.Type.ID() != types[i] {
			return fmt.Errorf("stuntgres: invalid object address OUT member")
		}
	}
	return nil
}
func (n objectAddressExpression) sql(name string, render func(BoundExpr) (string, error)) (string, error) {
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
func objectAddressTextArray(d *sqltype.Descriptor, names []string) (sqltype.Value, error) {
	values := make([]sqltype.Value, len(names))
	for i, name := range names {
		values[i] = sqltype.String(d.Element(), name)
	}
	var dims []sqltype.ArrayDimension
	if len(names) != 0 {
		dims = []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(names))}}
	}
	return sqltype.OwnedArrayValue(d, dims, values)
}
func (n objectAddressExpression) Eval(c *EvalContext) (sqltype.Value, error) {
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
		return sqltype.Null(n.usage.Type), nil
	}
	scope := c.liveScope()
	if scope == nil || scope.commentCatalog.state == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	if n.op == getObjectAddress {
		address, err := lookupObjectAddress(c.Context, scope, values)
		if err != nil {
			return sqltype.Value{}, err
		}
		fields := make([]sqltype.Value, 3)
		fields[0], err = sqltype.OIDValue(n.usage.Type.CompositeMember(0).Usage.Type, uint32(address.ClassID))
		if err != nil {
			return sqltype.Value{}, err
		}
		fields[1], err = sqltype.OIDValue(n.usage.Type.CompositeMember(1).Usage.Type, uint32(address.ObjectID))
		if err != nil {
			return sqltype.Value{}, err
		}
		fields[2], err = sqltype.Integer(n.usage.Type.CompositeMember(2).Usage.Type, int64(address.SubID))
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.OwnedCompositeValue(n.usage.Type, fields)
	}
	class, err := values[0].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	object, err := values[1].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	sub, err := values[2].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	address := ObjectAddress{OID(class), OID(object), int32(sub)}
	if n.op == describeObject && class == 0 && object == 0 {
		return sqltype.Null(n.usage.Type), nil
	}
	identity, err := identifyAddress(scope.commentCatalog, scope.commentRoles, scope.deparsePath, address)
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.op == describeObject {
		if !identity.found {
			return sqltype.Null(n.usage.Type), nil
		}
		return sqltype.String(n.usage.Type, identity.description), nil
	}
	fields := make([]sqltype.Value, n.usage.Type.CompositeLen())
	for i := range fields {
		fields[i] = sqltype.Null(n.usage.Type.CompositeMember(i).Usage.Type)
	}
	fields[0] = sqltype.String(n.usage.Type.CompositeMember(0).Usage.Type, identity.kind)
	if identity.found {
		if n.op == identifyObject {
			for i, text := range []string{identity.schema, identity.name, identity.identity} {
				if text != "" {
					fields[i+1] = sqltype.String(n.usage.Type.CompositeMember(i+1).Usage.Type, text)
				}
			}
		} else {
			fields[1], err = objectAddressTextArray(n.usage.Type.CompositeMember(1).Usage.Type, identity.names)
			if err != nil {
				return sqltype.Value{}, err
			}
			fields[2], err = objectAddressTextArray(n.usage.Type.CompositeMember(2).Usage.Type, identity.args)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
	}
	return sqltype.OwnedCompositeValue(n.usage.Type, fields)
}
