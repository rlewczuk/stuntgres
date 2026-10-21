package regobj

import (
	"strconv"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func OperatorSpelling(view catalog.Read, path []ids.ObjectAddress, groups exec.OperatorGroups, id ids.OID, alias sqltype.ID) string {
	descriptor, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id})
	if !ok {
		return strconv.FormatUint(uint64(id), 10)
	}
	// Temporary namespaces never participate in unqualified operator visibility.
	operatorPath := path
	for i, ns := range path {
		if d, ok := view.Namespace(ns); ok && d.Temporary != (ids.SessionToken{}) {
			operatorPath = append([]ids.ObjectAddress(nil), path[:i]...)
			for _, remaining := range path[i+1:] {
				if d, ok := view.Namespace(remaining); !ok || d.Temporary == (ids.SessionToken{}) {
					operatorPath = append(operatorPath, remaining)
				}
			}
			break
		}
	}
	candidates := VisibleOperators(groups, operatorPath, descriptor.Name())
	visible := len(candidates) == 1 && candidates[0].Address() == descriptor.Address()
	if alias == sqltype.Regoperator {
		visible = false
		for _, d := range candidates {
			if d.Left == descriptor.Left && d.Right == descriptor.Right {
				visible = d.Address() == descriptor.Address()
				break
			}
		}
	}
	name := descriptor.Name()
	if !visible {
		name = regclassIdentifier(view.Get(descriptor.Namespace()).Name()) + "." + name
	}
	if alias == sqltype.Regoperator {
		operand := func(id ids.OID) string {
			if id == 0 {
				return "NONE"
			}
			return TypeSpelling(view, path, id)
		}
		name += "(" + operand(descriptor.Left) + "," + operand(descriptor.Right) + ")"
	}
	return name
}

func CaptureOperatorOutputValue(rows *exec.FinalRows, view catalog.Read, path []ids.ObjectAddress, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsOperatorAlias() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := CaptureOperatorOutputValue(rows, view, path, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := CaptureOperatorOutputValue(rows, view, path, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	id, err := value.OperatorOID()
	if err != nil {
		return err
	}
	key := exec.OperatorOutputKey{ID: id, Alias: value.Type().BaseType().ID()}
	if _, ok := rows.OperatorOutput[key]; ok {
		return nil
	}
	if rows.OperatorGroups == nil {
		rows.OperatorGroups = CollectOperatorGroups(view)
	}
	if rows.OperatorOutput == nil {
		rows.OperatorOutput = make(map[exec.OperatorOutputKey]string)
	}
	rows.OperatorOutput[key] = OperatorSpelling(view, path, rows.OperatorGroups, ids.OID(id), key.Alias)
	rows.Output.Operator = rows
	return nil
}
