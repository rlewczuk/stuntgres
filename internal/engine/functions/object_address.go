package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type ObjectAddressOperation uint8

const (
	IdentifyObject ObjectAddressOperation = iota
	IdentifyObjectAddress
	GetObjectAddress
	DescribeObject
)

func (op ObjectAddressOperation) name() string {
	switch op {
	case IdentifyObject:
		return "pg_identify_object"
	case IdentifyObjectAddress:
		return "pg_identify_object_as_address"
	case GetObjectAddress:
		return "pg_get_object_address"
	case DescribeObject:
		return "pg_describe_object"
	}
	return ""
}
func (op ObjectAddressOperation) signature() ([3]sqltype.ID, [3]string) {
	if op == GetObjectAddress {
		return [3]sqltype.ID{sqltype.Text, 1009, 1009}, [3]string{"type", "object_names", "object_args"}
	}
	labels := [3]string{"classid", "objid", "objsubid"}
	if op == DescribeObject {
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

func (op ObjectAddressOperation) fields() ([]string, []sqltype.ID) {
	if op >= DescribeObject {
		return nil, nil
	}
	return objectAddressRecords[op].names, objectAddressRecords[op].types
}

// Arguments, labels and coercions retain written order independently of slots.
// The immutable recipe never retains a mutable catalog or a computed result.
type ObjectAddress struct {
	expr.Info
	op     ObjectAddressOperation
	Args   [3]expr.Expr
	Slots  [3]uint8
	Labels [3]string
}

func bindObjectAddress(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
		if i < 3 {
			operands[i] = operand
		}
		if i < len(names) {
			name := "unknown"
			if typ := operand.Usage.Type; typ != nil {
				name = typ.DiagnosticName()
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operation := IdentifyObject
	for operation <= DescribeObject && operation.name() != name {
		operation++
	}
	if !visible || operation > DescribeObject || len(raw.Args) != 3 || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	ids, labels := operation.signature()
	n := ObjectAddress{op: operation, Slots: [3]uint8{0, 1, 2}}
	var occupied [3]bool
	for i := range n.Args {
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
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
			n.Slots[i], n.Labels[i] = uint8(slot), label
		}
		slot := n.Slots[i]
		if occupied[slot] {
			return nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
		occupied[slot] = true
		if operands[i].Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(operands[i].Expression.ResultUsage(), b.Type(ids[slot]).Usage(), sqltype.Implicit); failure != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
		}
	}
	properties := expr.Properties{Volatility: expr.Stable}
	for i := range n.Args {
		target := b.Type(ids[n.Slots[i]]).Usage()
		arg, err := b.CoerceArgument(operands[i], CoercionRequest{Intent: FinalizeArgument, Target: target})
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage() != target {
			plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
			if failure != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
			arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[i], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
			if err != nil {
				return nil, err
			}
		}
		n.Args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	usage := b.Type(sqltype.Text).Usage()
	if operation != DescribeObject {
		labels, ids := operation.fields()
		members := make([]sqltype.CompositeMember, len(labels))
		for i, name := range labels {
			members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: b.Type(ids[i]).Usage()}
		}
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
	}
	n.Info, err = b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), CatalogRoutineProvider, operation.name(), ids[:]...)
	if err != nil {
		return nil, err
	}
	return n, nil
}
func (n ObjectAddress) Validate() error {
	if n.op > DescribeObject || !n.Usage.Valid() {
		return fmt.Errorf("stuntgres: invalid object address operation")
	}
	ids, Labels := n.op.signature()
	var seen [3]bool
	named := false
	properties := expr.Properties{Volatility: expr.Stable}
	for i, arg := range n.Args {
		slot := n.Slots[i]
		if slot >= 3 || seen[slot] || arg == nil || !arg.ResultUsage().Valid() || arg.ResultUsage() != arg.ResultUsage().Type.Usage() || arg.ResultUsage().Type.ID() != ids[slot] {
			return fmt.Errorf("stuntgres: invalid object address signature")
		}
		seen[slot] = true
		if n.Labels[i] != "" {
			if n.Labels[i] != Labels[slot] {
				return fmt.Errorf("stuntgres: invalid object address label")
			}
			named = true
		} else if named || int(slot) != i {
			return fmt.Errorf("stuntgres: invalid object address argument order")
		}
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if properties != n.Properties() {
		return fmt.Errorf("stuntgres: invalid object address properties")
	}
	if n.op == DescribeObject {
		if n.Usage.Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid object description result")
		}
		return nil
	}
	outLabels, types := n.op.fields()
	shape := n.Usage.Type
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

func (n ObjectAddress) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := ctx
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	null := false
	for i, arg := range n.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[n.Slots[i]] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	runtime, err := catalogInquiryRuntime(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.op == GetObjectAddress {
		address, err := runtime.LookupObjectAddress(values)
		if err != nil {
			return sqltype.Value{}, err
		}
		fields := make([]sqltype.Value, 3)
		fields[0], err = sqltype.OIDValue(n.Usage.Type.CompositeMember(0).Usage.Type, uint32(address.ClassID))
		if err != nil {
			return sqltype.Value{}, err
		}
		fields[1], err = sqltype.OIDValue(n.Usage.Type.CompositeMember(1).Usage.Type, uint32(address.ObjectID))
		if err != nil {
			return sqltype.Value{}, err
		}
		fields[2], err = sqltype.Integer(n.Usage.Type.CompositeMember(2).Usage.Type, int64(address.SubID))
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.OwnedCompositeValue(n.Usage.Type, fields)
	}
	switch n.op {
	case IdentifyObject:
		return runtime.IdentifyObject(values, n.Usage.Type)
	case IdentifyObjectAddress:
		return runtime.IdentifyObjectAddress(values, n.Usage.Type)
	case DescribeObject:
		return runtime.DescribeObject(values, n.Usage.Type)
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid object address operation")
}
