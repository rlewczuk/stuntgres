package regobj

import (
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TextSearchSpelling(view catalog.Read, path []ids.ObjectAddress, id uint32, alias sqltype.ID, qualified bool) string {
	if id == 0 {
		return "-"
	}
	class := ids.TSDictionaryClassID
	if alias == sqltype.Regconfig {
		class = ids.TSConfigurationClassID
	}
	d, ok := view.Find(ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(id)})
	if !ok {
		return strconv.FormatUint(uint64(id), 10)
	}
	visible := false
	if !qualified {
		for _, ns := range path {
			if namespace, ok := view.Namespace(ns); ok && namespace.Temporary != (ids.SessionToken{}) {
				continue
			}
			address := view.NameAddress(ids.NameKey{ClassID: class, Namespace: ns, Name: d.Name()})
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

func CaptureTextSearchOutputValue(rows *exec.FinalRows, view catalog.Read, path []ids.ObjectAddress, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegTS() {
		return nil
	}
	if value.Type().BaseType().RangeType() != nil {
		return value.VisitRangeEndpoints(func(leaf sqltype.Value) error { return CaptureTextSearchOutputValue(rows, view, path, leaf) })
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := CaptureTextSearchOutputValue(rows, view, path, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := CaptureTextSearchOutputValue(rows, view, path, value.ArrayElement(i)); err != nil {
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
	rows.RegTSOutput[key] = TextSearchSpelling(view, path, id, key.Alias, false)
	rows.Output.RegTS = rows
	return nil
}
