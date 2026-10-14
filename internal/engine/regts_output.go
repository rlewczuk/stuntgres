package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/sqltype"
)

type regTSOutputKey struct {
	id    uint32
	alias sqltype.ID
}

func regTSSpelling(view CatalogRead, path []ObjectAddress, id uint32, alias sqltype.ID, qualified bool) string {
	if id == 0 {
		return "-"
	}
	if alias != sqltype.Regdictionary {
		return strconv.FormatUint(uint64(id), 10)
	}
	d, ok := view.state.descriptors[ObjectAddress{ClassID: TSDictionaryClassID, ObjectID: OID(id)}].(*tsDictionaryDescriptor)
	if !ok {
		return strconv.FormatUint(uint64(id), 10)
	}
	visible := false
	if !qualified {
		for _, ns := range path {
			if namespace, ok := view.state.descriptors[ns].(*namespaceDescriptor); ok && namespace.temporary != (SessionToken{}) {
				continue
			}
			address := view.state.names[NameKey{ClassID: TSDictionaryClassID, Namespace: ns, Name: d.name}]
			if address != (ObjectAddress{}) {
				visible = address == d.address
				break
			}
		}
	}
	name := regclassIdentifier(d.name)
	if !visible {
		name = regclassIdentifier(view.state.descriptors[d.namespace].Name()) + "." + name
	}
	return name
}
func (s *StatementScope) formatRegTS(id uint32, alias sqltype.ID) (string, error) {
	if s == nil || s.commentCatalog.state == nil {
		return "", fmt.Errorf("stuntgres: missing text search output snapshot")
	}
	key := regTSOutputKey{id, alias}
	if text, ok := s.regTSOutput[key]; ok {
		return text, nil
	}
	text := regTSSpelling(s.commentCatalog, s.deparsePath, id, alias, false)
	if s.regTSOutput == nil {
		s.regTSOutput = make(map[regTSOutputKey]string)
	}
	s.regTSOutput[key] = text
	return text, nil
}
func (s *StatementScope) FormatRegconfig(id uint32) (string, error) {
	return s.formatRegTS(id, sqltype.Regconfig)
}
func (s *StatementScope) FormatRegdictionary(id uint32) (string, error) {
	return s.formatRegTS(id, sqltype.Regdictionary)
}
func (r *finalRows) formatRegTS(id uint32, alias sqltype.ID) (string, error) {
	text, ok := r.regTSOutput[regTSOutputKey{id, alias}]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing text search output snapshot")
	}
	return text, nil
}
func (r *finalRows) FormatRegconfig(id uint32) (string, error) {
	return r.formatRegTS(id, sqltype.Regconfig)
}
func (r *finalRows) FormatRegdictionary(id uint32) (string, error) {
	return r.formatRegTS(id, sqltype.Regdictionary)
}
func captureRegTSOutputValue(rows *finalRows, view CatalogRead, path []ObjectAddress, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegTS() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := captureRegTSOutputValue(rows, view, path, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := captureRegTSOutputValue(rows, view, path, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	id, err := value.RegTSOID()
	if err != nil {
		return err
	}
	key := regTSOutputKey{id, value.Type().BaseType().ID()}
	if _, ok := rows.regTSOutput[key]; ok {
		return nil
	}
	if rows.regTSOutput == nil {
		rows.regTSOutput = make(map[regTSOutputKey]string)
	}
	rows.regTSOutput[key] = regTSSpelling(view, path, id, key.alias, false)
	rows.output.RegTS = rows
	return nil
}
