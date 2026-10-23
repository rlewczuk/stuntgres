package regobj

import (
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func ObjectSpelling(view catalog.Read, path []ids.ObjectAddress, class, id ids.OID) string {
	if id == 0 {
		return "-"
	}
	d := view.Get(ids.ObjectAddress{ClassID: class, ObjectID: id})
	if d == nil {
		return strconv.FormatUint(uint64(id), 10)
	}
	name := regclassIdentifier(d.Name())
	if class == ids.NamespaceClassID || VisibleCollation(view, path, d) {
		return name
	}
	return regclassIdentifier(view.Get(d.Namespace()).Name()) + "." + name
}

func CaptureObjectOutputValue(rows *exec.FinalRows, view catalog.Read, path []ids.ObjectAddress, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegObject() {
		return nil
	}
	if value.Type().BaseType().RangeType() != nil {
		return value.VisitRangeEndpoints(func(leaf sqltype.Value) error { return CaptureObjectOutputValue(rows, view, path, leaf) })
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := CaptureObjectOutputValue(rows, view, path, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := CaptureObjectOutputValue(rows, view, path, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	address, err := catalog.AliasAddress(value)
	if err != nil {
		return err
	}
	if _, exists := rows.RegobjectOutput[address]; exists {
		return nil
	}
	if rows.RegobjectOutput == nil {
		rows.RegobjectOutput = make(map[ids.ObjectAddress]string)
	}
	rows.RegobjectOutput[address] = ObjectSpelling(view, path, address.ClassID, address.ObjectID)
	rows.Output.Regcollation, rows.Output.Regnamespace = rows, rows
	return nil
}
