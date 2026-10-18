package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type operatorOutputKey struct {
	id    uint32
	alias sqltype.ID
}

func operatorSpelling(view CatalogRead, path []ids.ObjectAddress, groups operatorGroups, id ids.OID, alias sqltype.ID) string {
	descriptor, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id})
	if !ok {
		return strconv.FormatUint(uint64(id), 10)
	}
	// Temporary namespaces never participate in unqualified operator visibility.
	operatorPath := path
	for i, ns := range path {
		if d, ok := view.Namespace(ns); ok && d.temporary != (ids.SessionToken{}) {
			operatorPath = append([]ids.ObjectAddress(nil), path[:i]...)
			for _, remaining := range path[i+1:] {
				if d, ok := view.Namespace(remaining); !ok || d.temporary == (ids.SessionToken{}) {
					operatorPath = append(operatorPath, remaining)
				}
			}
			break
		}
	}
	candidates := visibleOperators(groups, operatorPath, descriptor.name)
	visible := len(candidates) == 1 && candidates[0].address == descriptor.address
	if alias == sqltype.Regoperator {
		visible = false
		for _, d := range candidates {
			if d.left == descriptor.left && d.right == descriptor.right {
				visible = d.address == descriptor.address
				break
			}
		}
	}
	name := descriptor.name
	if !visible {
		name = regclassIdentifier(view.Get(descriptor.namespace).Name()) + "." + name
	}
	if alias == sqltype.Regoperator {
		operand := func(id ids.OID) string {
			if id == 0 {
				return "NONE"
			}
			return regtypeSpelling(view, path, id)
		}
		name += "(" + operand(descriptor.left) + "," + operand(descriptor.right) + ")"
	}
	return name
}
func (s *StatementScope) formatOperator(id uint32, alias sqltype.ID) (string, error) {
	if s == nil || s.commentCatalog.state == nil {
		return "", fmt.Errorf("stuntgres: missing operator output snapshot")
	}
	key := operatorOutputKey{id, alias}
	if text, ok := s.operatorOutput[key]; ok {
		return text, nil
	}
	if s.operatorGroups == nil {
		s.operatorGroups = collectOperatorGroups(s.commentCatalog)
	}
	text := operatorSpelling(s.commentCatalog, s.deparsePath, s.operatorGroups, ids.OID(id), alias)
	if s.operatorOutput == nil {
		s.operatorOutput = make(map[operatorOutputKey]string)
	}
	s.operatorOutput[key] = text
	return text, nil
}
func (s *StatementScope) FormatRegoper(id uint32) (string, error) {
	return s.formatOperator(id, sqltype.Regoper)
}
func (s *StatementScope) FormatRegoperator(id uint32) (string, error) {
	return s.formatOperator(id, sqltype.Regoperator)
}
func (r *finalRows) formatOperator(id uint32, alias sqltype.ID) (string, error) {
	text, ok := r.operatorOutput[operatorOutputKey{id, alias}]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing operator output snapshot")
	}
	return text, nil
}
func (r *finalRows) FormatRegoper(id uint32) (string, error) {
	return r.formatOperator(id, sqltype.Regoper)
}
func (r *finalRows) FormatRegoperator(id uint32) (string, error) {
	return r.formatOperator(id, sqltype.Regoperator)
}
func captureOperatorOutputValue(rows *finalRows, view CatalogRead, path []ids.ObjectAddress, value sqltype.Value) error {
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
	key := operatorOutputKey{id, value.Type().BaseType().ID()}
	if _, ok := rows.operatorOutput[key]; ok {
		return nil
	}
	if rows.operatorGroups == nil {
		rows.operatorGroups = collectOperatorGroups(view)
	}
	if rows.operatorOutput == nil {
		rows.operatorOutput = make(map[operatorOutputKey]string)
	}
	rows.operatorOutput[key] = operatorSpelling(view, path, rows.operatorGroups, ids.OID(id), key.alias)
	rows.output.Operator = rows
	return nil
}
