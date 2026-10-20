package engine

import (
	"strconv"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func operatorSpelling(view catalog.Read, path []ids.ObjectAddress, groups exec.OperatorGroups, id ids.OID, alias sqltype.ID) string {
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
	candidates := visibleOperators(groups, operatorPath, descriptor.Name())
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
			return regtypeSpelling(view, path, id)
		}
		name += "(" + operand(descriptor.Left) + "," + operand(descriptor.Right) + ")"
	}
	return name
}
func (s *StatementScope) formatOperator(id uint32, alias sqltype.ID) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		if alias == sqltype.Regoperator {
			return exec.DetachedPresentation{}.FormatRegoperator(id)
		}
		return exec.DetachedPresentation{}.FormatRegoper(id)
	}
	key := exec.OperatorOutputKey{ID: id, Alias: alias}
	if text, ok := s.presentationOrZero().operators[key]; ok {
		return text, nil
	}
	if s.presentationOrZero().operatorGroups == nil {
		s.presentation().operatorGroups = collectOperatorGroups(s.inquiry.commentCatalog)
	}
	text := operatorSpelling(s.inquiry.commentCatalog, s.inquiry.deparsePath, s.presentationOrZero().operatorGroups, ids.OID(id), alias)
	if s.presentationOrZero().operators == nil {
		s.presentation().operators = make(map[exec.OperatorOutputKey]string)
	}
	s.presentation().operators[key] = text
	return text, nil
}
func (s *StatementScope) FormatRegoper(id uint32) (string, error) {
	return s.formatOperator(id, sqltype.Regoper)
}
func (s *StatementScope) FormatRegoperator(id uint32) (string, error) {
	return s.formatOperator(id, sqltype.Regoperator)
}
func captureOperatorOutputValue(rows *exec.FinalRows, view catalog.Read, path []ids.ObjectAddress, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsOperatorAlias() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := captureOperatorOutputValue(rows, view, path, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := captureOperatorOutputValue(rows, view, path, value.ArrayElement(i)); err != nil {
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
		rows.OperatorGroups = collectOperatorGroups(view)
	}
	if rows.OperatorOutput == nil {
		rows.OperatorOutput = make(map[exec.OperatorOutputKey]string)
	}
	rows.OperatorOutput[key] = operatorSpelling(view, path, rows.OperatorGroups, ids.OID(id), key.Alias)
	rows.Output.Operator = rows
	return nil
}
