package engine

import (
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func RoutineSpelling(view catalog.Read, path []ids.ObjectAddress, id ids.OID, signatureQualified, qualified bool) string {
	if id == 0 {
		return "-"
	}
	procedure, ok := RoutineIdentityByOID(view, id)
	if !ok {
		return strconv.FormatUint(uint64(id), 10)
	}
	var signature *catalog.ProcedureSignature
	if signatureQualified {
		signature = &procedure.Signature
	}
	selected, count := SelectRoutine(view, analyze.NamespaceSelection{Path: path}, procedure.Name, signature)
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
	WriteRoutineArguments(&text, view, path, procedure.Signature, false)
	text.WriteByte(')')
	return text.String()
}

// Signatures come from the validated catalog identity inventory. Diagnostics use
// canonical builtin names and diagnostic path authority; regprocedure output
// retains its own type visibility rules.
func WriteRoutineArguments(text *strings.Builder, view catalog.Read, path []ids.ObjectAddress, signature catalog.ProcedureSignature, diagnostic bool) {
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
				text.WriteString(TypeSpelling(view, path, ids.OID(oid)))
			}
		}
	}
}

func ProcSpelling(view catalog.Read, path []ids.ObjectAddress, id ids.OID) string {
	return RoutineSpelling(view, path, id, false, false)
}

func CaptureProcOutputValue(rows *exec.FinalRows, view catalog.Read, path []ids.ObjectAddress, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegproc() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := CaptureProcOutputValue(rows, view, path, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := CaptureProcOutputValue(rows, view, path, value.ArrayElement(i)); err != nil {
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
	output := rows.RegprocOutput
	if signature {
		output = rows.RegprocedureOutput
	}
	if _, ok := output[id]; ok {
		return nil
	}
	if output == nil {
		output = make(map[uint32]string)
		if signature {
			rows.RegprocedureOutput = output
		} else {
			rows.RegprocOutput = output
		}
	}
	output[id] = RoutineSpelling(view, path, ids.OID(id), signature, false)
	rows.Output.Regproc = rows
	return nil
}

// Catalog renderers use captured authority, never a live Rows lookup.
type catalogRoutineOutput struct {
	View      catalog.Read
	Path      []ids.ObjectAddress
	qualified bool
}

func (r catalogRoutineOutput) FormatRegproc(id uint32) (string, error) {
	return RoutineSpelling(r.View, r.Path, ids.OID(id), false, r.qualified), nil
}

func (r catalogRoutineOutput) FormatRegprocedure(id uint32) (string, error) {
	return RoutineSpelling(r.View, r.Path, ids.OID(id), true, r.qualified), nil
}
