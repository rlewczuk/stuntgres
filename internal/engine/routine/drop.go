package routine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// selectDropRoutine uses only real descriptors. Filtering precedes namespace
// masking for bare names; exact signatures deliberately check kind afterwards.
func selectDropRoutine(view catalog.Read, namespaces []ids.ObjectAddress, name, kind string, exact *catalog.ProcedureSignature) (*catalog.Procedure, int) {
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

func dropRoutineNoun(kind string) string {
	if kind == "procedure" {
		return kind
	}
	return "function"
}

func dropRoutineArguments(h DDLHost, auth ddl.Authority, target parser.GrantRoutineTarget) (catalog.ProcedureSignature, string, string, error) {
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
		// SQL aliases in PostgreSQL's utility notice retain their parser's
		// qualified internal spelling, unlike canonical error signatures.
		if argument.Type.System {
			written.WriteString("pg_catalog.")
			written.WriteString(argument.Type.Name)
		} else {
			if argument.Type.Schema != nil {
				written.WriteString(catalog.DependencyIdentifier(argument.Type.Schema.Text))
				written.WriteByte('.')
			}
			written.WriteString(catalog.DependencyIdentifier(argument.Type.Name))
		}
		for range argument.Type.ArrayBounds {
			written.WriteString("[]")
		}
	}
	return catalog.ProcedureSignature(signature.String()), canonical.String(), written.String(), nil
}

// StageDropRoutine records all roots before the shared dependency owner mutates
// anything. A late resolution, permission or protection error publishes nothing.
func StageDropRoutine(h DDLHost, auth ddl.Authority, s *parser.DropRoutine) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Targets))
	for _, target := range s.Targets {
		namespaces, err := h.SearchNamespaces(auth, target.Name, ids.ProcedureClassID)
		var signature catalog.ProcedureSignature
		var canonical, written string
		if err == nil && target.SignatureSpecified {
			signature, canonical, written, err = dropRoutineArguments(h, auth, target)
		}
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok && s.IfExists && (d.Code == "3F000" || d.Code == "42704") {
				h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: d.Message + ", skipping"})
				continue
			}
			return err
		}
		var exact *catalog.ProcedureSignature
		if target.SignatureSpecified {
			exact = &signature
		}
		name := target.Name.Parts[len(target.Name.Parts)-1].Text
		d, count := selectDropRoutine(h.AccessView(auth).Catalog, namespaces, name, s.Kind, exact)
		display := catalog.ObjectNameText(target.Name)
		noun := dropRoutineNoun(s.Kind)
		if count == 0 {
			if s.IfExists {
				h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: s.Kind + " " + display + "(" + written + ") does not exist, skipping"})
				continue
			}
			if !target.SignatureSpecified {
				return sqlError("42883", fmt.Sprintf(`could not find a %s named "%s"`, noun, display))
			}
			return sqlError("42883", noun+" "+display+"("+canonical+") does not exist")
		}
		if count > 1 {
			diagnostic := sqlError("42725", fmt.Sprintf(`%s name "%s" is not unique`, s.Kind, display)).(*sqlstate.Error)
			if !target.SignatureSpecified {
				diagnostic.Hint = "Specify the argument list to select the " + s.Kind + " unambiguously."
			}
			return diagnostic
		}
		if s.Kind == "procedure" && d.RoutineKind != catalog.ProcedureRoutine || s.Kind == "function" && d.RoutineKind == catalog.ProcedureRoutine {
			return sqlError("42809", display+"("+canonical+") is not a "+noun)
		}
		if s.Kind == "function" && d.RoutineKind == catalog.AggregateRoutine {
			diagnostic := sqlError("42809", fmt.Sprintf(`"%s" is an aggregate function`, display)).(*sqlstate.Error)
			diagnostic.Hint = "Use DROP AGGREGATE to drop aggregate functions."
			return diagnostic
		}
		namespaceOwner := ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, d.Namespace()), OwnerOnly: true}) == nil
		if !namespaceOwner {
			if err := ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, d.Address()), OwnerOnly: true}); err != nil {
				if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
					return sqlError("42501", "must be owner of "+noun+" "+display)
				}
				return err
			}
		}
		object, err := h.DependencyObject(auth, d.Address())
		if err != nil {
			return err
		}
		if d.Seeded() {
			return sqlError("2BP01", "cannot drop "+object+" because it is required by the database system")
		}
		if d.Extension != (ids.ObjectAddress{}) {
			ext, ok := h.AccessView(auth).Catalog.Descriptor(d.Extension)
			if !ok {
				return fmt.Errorf("stuntgres: routine extension is absent")
			}
			diagnostic := sqlError("2BP01", "cannot drop "+object+" because extension "+catalog.DependencyIdentifier(ext.Name())+" requires it").(*sqlstate.Error)
			diagnostic.Hint = "You can drop extension " + catalog.DependencyIdentifier(ext.Name()) + " instead."
			return diagnostic
		}
		if !seen[d.Address()] {
			seen[d.Address()] = true
			h.DropTarget(d.Address())
		}
	}
	return h.RecordDependencyPath(auth)
}
