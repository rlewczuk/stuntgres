package routine

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// StageDropRoutine records all roots before the shared dependency owner mutates
// anything. A late resolution, permission or protection error publishes nothing.
func StageDropRoutine(h DDLHost, auth ddl.Authority, s *parser.DropRoutine) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Targets))
	for _, target := range s.Targets {
		namespaces, err := h.SearchNamespaces(auth, target.Name, ids.ProcedureClassID)
		var signature catalog.ProcedureSignature
		var canonical, written string
		if err == nil && target.SignatureSpecified {
			signature, canonical, written, err = utilityRoutineArguments(h, auth, target)
		}
		if err != nil {
			var d *sqlstate.Error
			if s.IfExists && errors.As(err, &d) && (d.Code == "3F000" || d.Code == "42704") {
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
		d, count := selectUtilityRoutine(h.AccessView(auth).Catalog, namespaces, name, s.Kind, exact)
		display := catalog.ObjectNameText(target.Name)
		noun := utilityRoutineNoun(s.Kind)
		if count == 0 {
			if s.IfExists {
				h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: s.Kind + " " + display + "(" + written + ") does not exist, skipping"})
				continue
			}
			if !target.SignatureSpecified {
				return sqlError("42883", fmt.Sprintf(`could not find a %s named "%s"`, noun, display))
			}
			if s.Kind == "aggregate" && canonical == "" {
				canonical = "*"
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
		if s.Kind == "aggregate" && d.RoutineKind != catalog.AggregateRoutine {
			return sqlError("42809", "function "+display+"("+canonical+") is not an aggregate")
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
		if owner := catalog.RangeConstructorOwner(d); owner != (ids.ObjectAddress{}) {
			typeObject, err := h.DependencyObject(auth, owner)
			if err != nil {
				return err
			}
			diagnostic := sqlError("2BP01", "cannot drop "+object+" because "+typeObject+" requires it").(*sqlstate.Error)
			diagnostic.Hint = "You can drop " + typeObject + " instead."
			return diagnostic
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
