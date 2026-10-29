package regobj

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TypeSpelling(view catalog.Read, path []ids.ObjectAddress, id ids.OID) string {
	return catalog.TypeSpelling(view, path, id)
}

func captureRegtypeOutputValue(rows *exec.FinalRows, view catalog.Read, path []ids.ObjectAddress, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegtype() {
		return nil
	}
	if value.Type().BaseType().RangeType() != nil {
		return value.VisitRangeEndpoints(func(leaf sqltype.Value) error { return captureRegtypeOutputValue(rows, view, path, leaf) })
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := captureRegtypeOutputValue(rows, view, path, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := captureRegtypeOutputValue(rows, view, path, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	id, err := value.TypeOID()
	if err != nil {
		return err
	}
	if _, ok := rows.RegtypeOutput[id]; ok {
		return nil
	}
	if rows.RegtypeOutput == nil {
		rows.RegtypeOutput = make(map[uint32]string)
	}
	rows.RegtypeOutput[id] = TypeSpelling(view, path, ids.OID(id))
	rows.Output.Regtype = rows
	return nil
}

type TypeOutput struct {
	View catalog.Read
	Path []ids.ObjectAddress
}

func (r TypeOutput) FormatRegtype(id uint32) (string, error) {
	return TypeSpelling(r.View, r.Path, ids.OID(id)), nil
}
