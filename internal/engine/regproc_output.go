package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/sqltype"
)

func regprocSpelling(view CatalogRead, path []ObjectAddress, id OID) string {
	if id == 0 {
		return "-"
	}
	procedure, ok := view.state.descriptors[ObjectAddress{ClassID: ProcedureClassID, ObjectID: id}].(*procedureDescriptor)
	if !ok {
		return strconv.FormatUint(uint64(id), 10)
	}
	visible := false
	for _, namespace := range path {
		if ns, ok := view.state.descriptors[namespace].(*namespaceDescriptor); ok && ns.temporary != (SessionToken{}) {
			continue
		}
		count, matching := 0, false
		for _, descriptor := range view.state.descriptors {
			p, ok := descriptor.(*procedureDescriptor)
			if ok && p.Namespace() == namespace && p.name == procedure.name {
				count++
				matching = matching || p.address == procedure.address
			}
		}
		if count != 0 {
			visible = count == 1 && matching
			break
		}
	}
	name := regclassIdentifier(procedure.name)
	if visible {
		return name
	}
	return regclassIdentifier(view.state.descriptors[procedure.Namespace()].Name()) + "." + name
}

func (s *StatementScope) FormatRegproc(id uint32) (string, error) {
	if s == nil || s.commentCatalog.state == nil {
		return "", fmt.Errorf("stuntgres: missing regproc output snapshot")
	}
	if text, ok := s.regprocOutput[id]; ok {
		return text, nil
	}
	text := regprocSpelling(s.commentCatalog, s.deparsePath, OID(id))
	if s.regprocOutput == nil {
		s.regprocOutput = make(map[uint32]string)
	}
	s.regprocOutput[id] = text
	return text, nil
}

func (r *finalRows) FormatRegproc(id uint32) (string, error) {
	text, ok := r.regprocOutput[id]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing retained regproc presentation")
	}
	return text, nil
}

func captureRegprocOutputValue(rows *finalRows, view CatalogRead, path []ObjectAddress, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegproc() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := captureRegprocOutputValue(rows, view, path, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := captureRegprocOutputValue(rows, view, path, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	id, err := value.ProcedureOID()
	if err != nil {
		return err
	}
	if _, ok := rows.regprocOutput[id]; ok {
		return nil
	}
	if rows.regprocOutput == nil {
		rows.regprocOutput = make(map[uint32]string)
	}
	rows.regprocOutput[id] = regprocSpelling(view, path, OID(id))
	rows.output.Regproc = rows
	return nil
}
