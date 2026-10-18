package engine

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func routineSpelling(view CatalogRead, path []ids.ObjectAddress, id ids.OID, signatureQualified, qualified bool) string {
	if id == 0 {
		return "-"
	}
	procedure, ok := routineIdentityByOID(view, id)
	if !ok {
		return strconv.FormatUint(uint64(id), 10)
	}
	var signature *procedureSignature
	if signatureQualified {
		signature = &procedure.Signature
	}
	selected, count := selectRoutine(view, namespaceSelection{path: path}, procedure.Name, signature)
	name := regclassIdentifier(procedure.Name)
	if qualified || count != 1 || selected.ID != id {
		namespace := view.Get(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: procedure.Namespace})
		name = regclassIdentifier(namespace.Name()) + "." + name
	}
	if !signatureQualified {
		return name
	}
	var text strings.Builder
	text.WriteString(name)
	text.WriteByte('(')
	if procedure.Signature != "" {
		first := true
		for arg := range strings.SplitSeq(string(procedure.Signature), ",") {
			if !first {
				text.WriteByte(',')
			}
			first = false
			oid, _ := strconv.ParseUint(arg, 10, 32) // validated identity inventory
			text.WriteString(regtypeSpelling(view, path, ids.OID(oid)))
		}
	}
	text.WriteByte(')')
	return text.String()
}

func regprocSpelling(view CatalogRead, path []ids.ObjectAddress, id ids.OID) string {
	return routineSpelling(view, path, id, false, false)
}

func (s *StatementScope) FormatRegproc(id uint32) (string, error) {
	if s == nil || s.commentCatalog.state == nil {
		return "", fmt.Errorf("stuntgres: missing regproc output snapshot")
	}
	if text, ok := s.regprocOutput[id]; ok {
		return text, nil
	}
	text := regprocSpelling(s.commentCatalog, s.deparsePath, ids.OID(id))
	if s.regprocOutput == nil {
		s.regprocOutput = make(map[uint32]string)
	}
	s.regprocOutput[id] = text
	return text, nil
}
func (s *StatementScope) FormatRegprocedure(id uint32) (string, error) {
	if s == nil || s.commentCatalog.state == nil {
		return "", fmt.Errorf("stuntgres: missing regprocedure output snapshot")
	}
	if text, ok := s.regprocedureOutput[id]; ok {
		return text, nil
	}
	text := routineSpelling(s.commentCatalog, s.deparsePath, ids.OID(id), true, false)
	if s.regprocedureOutput == nil {
		s.regprocedureOutput = make(map[uint32]string)
	}
	s.regprocedureOutput[id] = text
	return text, nil
}
func (r *finalRows) FormatRegproc(id uint32) (string, error) {
	text, ok := r.regprocOutput[id]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing retained regproc presentation")
	}
	return text, nil
}
func (r *finalRows) FormatRegprocedure(id uint32) (string, error) {
	text, ok := r.regprocedureOutput[id]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing retained regprocedure presentation")
	}
	return text, nil
}

func captureRegprocOutputValue(rows *finalRows, view CatalogRead, path []ids.ObjectAddress, value sqltype.Value) error {
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
	signature := value.Type().BaseType().ID() == sqltype.Regprocedure
	output := rows.regprocOutput
	if signature {
		output = rows.regprocedureOutput
	}
	if _, ok := output[id]; ok {
		return nil
	}
	if output == nil {
		output = make(map[uint32]string)
		if signature {
			rows.regprocedureOutput = output
		} else {
			rows.regprocOutput = output
		}
	}
	output[id] = routineSpelling(view, path, ids.OID(id), signature, false)
	rows.output.Regproc = rows
	return nil
}

// Catalog renderers use captured authority, never a live Rows lookup.
type catalogRoutineOutput struct {
	view      CatalogRead
	path      []ids.ObjectAddress
	qualified bool
}

func (r catalogRoutineOutput) FormatRegproc(id uint32) (string, error) {
	return routineSpelling(r.view, r.path, ids.OID(id), false, r.qualified), nil
}
func (r catalogRoutineOutput) FormatRegprocedure(id uint32) (string, error) {
	return routineSpelling(r.view, r.path, ids.OID(id), true, r.qualified), nil
}
