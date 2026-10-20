package engine

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func routineSpelling(view catalog.Read, path []ids.ObjectAddress, id ids.OID, signatureQualified, qualified bool) string {
	if id == 0 {
		return "-"
	}
	procedure, ok := routineIdentityByOID(view, id)
	if !ok {
		return strconv.FormatUint(uint64(id), 10)
	}
	var signature *catalog.ProcedureSignature
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
	writeRoutineArguments(&text, view, path, procedure.Signature, false)
	text.WriteByte(')')
	return text.String()
}

// Signatures come from the validated catalog identity inventory. Diagnostics use
// canonical builtin names and diagnostic path authority; regprocedure output
// retains its own type visibility rules.
func writeRoutineArguments(text *strings.Builder, view catalog.Read, path []ids.ObjectAddress, signature catalog.ProcedureSignature, diagnostic bool) {
	first := true
	if signature != "" {
		for arg := range strings.SplitSeq(string(signature), ",") {
			if !first {
				text.WriteByte(',')
			}
			first = false
			oid, _ := strconv.ParseUint(arg, 10, 32)
			if diagnostic {
				typ := view.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(oid)}).(*catalog.Type)
				if typ.SQLType.NamespaceName() == "pg_catalog" {
					text.WriteString(typ.SQLType.DiagnosticName())
				} else {
					text.WriteString(catalog.TypeDiagnosticNameAt(view, typ, path))
				}
			} else {
				text.WriteString(regtypeSpelling(view, path, ids.OID(oid)))
			}
		}
	}
}

func regprocSpelling(view catalog.Read, path []ids.ObjectAddress, id ids.OID) string {
	return routineSpelling(view, path, id, false, false)
}

func (s *StatementScope) FormatRegproc(id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		return "", fmt.Errorf("stuntgres: missing regproc output snapshot")
	}
	if text, ok := s.regprocOutput[id]; ok {
		return text, nil
	}
	text := regprocSpelling(s.inquiry.commentCatalog, s.inquiry.deparsePath, ids.OID(id))
	if s.regprocOutput == nil {
		s.regprocOutput = make(map[uint32]string)
	}
	s.regprocOutput[id] = text
	return text, nil
}
func (s *StatementScope) FormatRegprocedure(id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		return "", fmt.Errorf("stuntgres: missing regprocedure output snapshot")
	}
	if text, ok := s.regprocedureOutput[id]; ok {
		return text, nil
	}
	text := routineSpelling(s.inquiry.commentCatalog, s.inquiry.deparsePath, ids.OID(id), true, false)
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

func captureRegprocOutputValue(rows *finalRows, view catalog.Read, path []ids.ObjectAddress, value sqltype.Value) error {
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
	view      catalog.Read
	path      []ids.ObjectAddress
	qualified bool
}

func (r catalogRoutineOutput) FormatRegproc(id uint32) (string, error) {
	return routineSpelling(r.view, r.path, ids.OID(id), false, r.qualified), nil
}
func (r catalogRoutineOutput) FormatRegprocedure(id uint32) (string, error) {
	return routineSpelling(r.view, r.path, ids.OID(id), true, r.qualified), nil
}
