package routine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
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
	if kind == "procedure" || kind == "aggregate" {
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
		canonical.WriteString(catalog.TypeDiagnosticNameAt(view, nil, typ, path))
		written.WriteString(writtenRoutineTypeName(argument.Type))
	}
	return catalog.ProcedureSignature(signature.String()), canonical.String(), written.String(), nil
}

func utilityRoutineSignatureDiagnostic(h DDLHost, auth ddl.Authority, signature catalog.ProcedureSignature) (string, error) {
	if signature == "" {
		return "", nil
	}
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
		text.WriteString(catalog.TypeDiagnosticNameAt(view, nil, typ, path))
	}
	return text.String(), nil
}

// ResolveUtilityRoutine is the shared typed utility identity path. Expression
// lookup and pg_get_object_address do not own ALTER's bare-name/kind rules.
func ResolveUtilityRoutine(h DDLHost, auth ddl.Authority, kind string, target parser.GrantRoutineTarget) (*catalog.Procedure, string, error) {
	namespaces, err := h.SearchNamespaces(auth, target.Name, ids.ProcedureClassID)
	if err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "3F000" {
			copy := *diagnostic
			copy.Position = ""
			err = &copy
		}
		return nil, "", err
	}
	var signature catalog.ProcedureSignature
	var canonical string
	var exact *catalog.ProcedureSignature
	if target.SignatureSpecified {
		signature, canonical, _, err = utilityRoutineArguments(h, auth, target)
		if err != nil {
			return nil, "", err
		}
		exact = &signature
	}
	d, count := selectUtilityRoutine(h.AccessView(auth).Catalog, namespaces, target.Name.Parts[len(target.Name.Parts)-1].Text, kind, exact)
	display, noun := catalog.ObjectNameText(target.Name), utilityRoutineNoun(kind)
	if count == 0 {
		if !target.SignatureSpecified {
			return nil, "", sqlError("42883", fmt.Sprintf(`could not find a %s named "%s"`, noun, display))
		}
		if kind == "aggregate" && canonical == "" {
			canonical = "*"
		}
		return nil, "", sqlError("42883", noun+" "+display+"("+canonical+") does not exist")
	}
	if count > 1 {
		return nil, "", &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42725", Message: fmt.Sprintf(`%s name "%s" is not unique`, kind, display), Hint: "Specify the argument list to select the " + kind + " unambiguously."}
	}
	if kind == "procedure" && d.RoutineKind != catalog.ProcedureRoutine || kind == "function" && d.RoutineKind == catalog.ProcedureRoutine {
		return nil, "", sqlError("42809", display+"("+canonical+") is not a "+noun)
	}
	if kind == "aggregate" && d.RoutineKind != catalog.AggregateRoutine {
		return nil, "", sqlError("42809", "function "+display+"("+canonical+") is not an aggregate")
	}
	return d, canonical, nil
}
