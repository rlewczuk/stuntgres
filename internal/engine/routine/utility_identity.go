package routine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// selectUtilityRoutine uses only real descriptors. Filtering precedes namespace
// masking for bare names; exact signatures deliberately check kind afterwards.
func selectUtilityRoutine(view catalog.Read, namespaces []ids.ObjectAddress, name, kind string, exact *catalog.ProcedureSignature) (*catalog.Procedure, int) {
	var selected *catalog.Procedure
	count := 0
	seen := make(map[catalog.ProcedureSignature]bool)
	for _, ns := range namespaces {
		for p := range catalog.DefinitionProceduresNamedAt(view, nil, ns, name) {
			if exact != nil {
				if p.Signature == *exact {
					return p, 1
				}
				continue
			}
			if kind == "procedure" && p.RoutineKind != catalog.ProcedureRoutine || kind == "function" && p.RoutineKind == catalog.ProcedureRoutine {
				continue
			}
			if seen[p.Signature] {
				continue
			}
			seen[p.Signature] = true
			selected, count = p, count+1
		}
	}
	return selected, count
}

func utilityRoutineNoun(kind string) string {
	if kind == "procedure" {
		return kind
	}
	return "function"
}

// writtenRoutineTypeName preserves the parsed utility spelling, including
// PostgreSQL's internal qualification of SQL type aliases.
func writtenRoutineTypeName(raw parser.TypeName) string {
	var name strings.Builder
	if raw.System {
		name.WriteString("pg_catalog.")
		name.WriteString(raw.Name)
	} else {
		if raw.Schema != nil {
			name.WriteString(catalog.DependencyIdentifier(raw.Schema.Text))
			name.WriteByte('.')
		}
		name.WriteString(catalog.DependencyIdentifier(raw.Name))
	}
	for range raw.ArrayBounds {
		name.WriteString("[]")
	}
	return name.String()
}

func utilityRoutineArguments(h DDLHost, auth ddl.Authority, target parser.GrantRoutineTarget) (catalog.ProcedureSignature, string, string, error) {
	view := h.AccessView(auth).Catalog
	path, err := auth.ReferencePath()
	if err != nil {
		return "", "", "", err
	}
	var signature, canonical, written strings.Builder
	count := 0
	for _, argument := range target.Arguments {
		if argument.Mode == "out" {
			continue
		}
		count++
		if count > 100 {
			return "", "", "", sqlError("54023", "functions cannot have more than 100 arguments")
		}
		id, err := h.ResolveRegtype(auth, argument.Type)
		if err != nil {
			return "", "", "", err
		}
		if count != 1 {
			signature.WriteByte(',')
			canonical.WriteString(", ")
			written.WriteByte(',')
		}
		var digits [20]byte
		signature.Write(strconv.AppendUint(digits[:0], uint64(id), 10))
		typ := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
		if typ.SQLType.IsUserDefined() {
			canonical.WriteString(catalog.TypeDiagnosticNameAt(view, typ, path))
		} else {
			canonical.WriteString(typ.SQLType.DiagnosticName())
		}
		written.WriteString(writtenRoutineTypeName(argument.Type))
	}
	return catalog.ProcedureSignature(signature.String()), canonical.String(), written.String(), nil
}

func utilityRoutineSignatureDiagnostic(h DDLHost, auth ddl.Authority, signature catalog.ProcedureSignature) (string, error) {
	view := h.AccessView(auth).Catalog
	path, err := auth.ReferencePath()
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for part := range strings.SplitSeq(string(signature), ",") {
		id, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return "", fmt.Errorf("stuntgres: invalid routine signature")
		}
		typ, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
		if !ok {
			return "", fmt.Errorf("stuntgres: missing routine input type")
		}
		if text.Len() != 0 {
			text.WriteString(", ")
		}
		if typ.SQLType.IsUserDefined() {
			text.WriteString(catalog.TypeDiagnosticNameAt(view, typ, path))
		} else {
			text.WriteString(typ.SQLType.DiagnosticName())
		}
	}
	return text.String(), nil
}
