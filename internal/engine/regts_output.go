package engine

import (
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func regTSSpelling(view catalog.Read, path []ids.ObjectAddress, id uint32, alias sqltype.ID, qualified bool) string {
	if id == 0 {
		return "-"
	}
	if alias != sqltype.Regdictionary {
		return strconv.FormatUint(uint64(id), 10)
	}
	d, ok := view.TSDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: ids.OID(id)})
	if !ok {
		return strconv.FormatUint(uint64(id), 10)
	}
	visible := false
	if !qualified {
		for _, ns := range path {
			if namespace, ok := view.Namespace(ns); ok && namespace.Temporary != (ids.SessionToken{}) {
				continue
			}
			address := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ns, Name: d.Name()})
			if address != (ids.ObjectAddress{}) {
				visible = address == d.Address()
				break
			}
		}
	}
	name := regclassIdentifier(d.Name())
	if !visible {
		name = regclassIdentifier(view.Get(d.Namespace()).Name()) + "." + name
	}
	return name
}
func (s *StatementScope) formatRegTS(id uint32, alias sqltype.ID) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		if alias == sqltype.Regdictionary {
			return exec.DetachedPresentation{}.FormatRegdictionary(id)
		}
		return exec.DetachedPresentation{}.FormatRegconfig(id)
	}
	key := exec.RegTSOutputKey{ID: id, Alias: alias}
	if text, ok := s.presentationOrZero().regTS[key]; ok {
		return text, nil
	}
	text := regTSSpelling(s.inquiry.commentCatalog, s.inquiry.deparsePath, id, alias, false)
	if s.presentationOrZero().regTS == nil {
		s.presentation().regTS = make(map[exec.RegTSOutputKey]string)
	}
	s.presentation().regTS[key] = text
	return text, nil
}
func (s *StatementScope) FormatRegconfig(id uint32) (string, error) {
	return s.formatRegTS(id, sqltype.Regconfig)
}
func (s *StatementScope) FormatRegdictionary(id uint32) (string, error) {
	return s.formatRegTS(id, sqltype.Regdictionary)
}
func captureRegTSOutputValue(rows *exec.FinalRows, view catalog.Read, path []ids.ObjectAddress, value sqltype.Value) error {
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
	key := exec.RegTSOutputKey{ID: id, Alias: value.Type().BaseType().ID()}
	if _, ok := rows.RegTSOutput[key]; ok {
		return nil
	}
	if rows.RegTSOutput == nil {
		rows.RegTSOutput = make(map[exec.RegTSOutputKey]string)
	}
	rows.RegTSOutput[key] = regTSSpelling(view, path, id, key.Alias, false)
	rows.Output.RegTS = rows
	return nil
}
