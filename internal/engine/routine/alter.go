package routine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// StageAlterRoutine changes a descriptor in place by address. Declaration and
// provider payloads remain immutable; the shared stage owns publication/fences.
func StageAlterRoutine(h DDLHost, auth ddl.Authority, s *parser.AlterRoutine) error {
	target := s.Routine
	namespaces, err := h.SearchNamespaces(auth, target.Name, ids.ProcedureClassID)
	if err != nil {
		// This utility's catalog namespace lookup has no expression cursor.
		// Preserve all other diagnostics, including argument type positions.
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "3F000" {
			copy := *diagnostic
			copy.Position = ""
			err = &copy
		}
		return err
	}
	var signature catalog.ProcedureSignature
	var canonical string
	var exact *catalog.ProcedureSignature
	if target.SignatureSpecified {
		signature, canonical, _, err = utilityRoutineArguments(h, auth, target)
		if err != nil {
			return err
		}
		exact = &signature
	}
	d, count := selectUtilityRoutine(h.AccessView(auth).Catalog, namespaces, target.Name.Parts[len(target.Name.Parts)-1].Text, s.Kind, exact)
	display, noun := catalog.ObjectNameText(target.Name), utilityRoutineNoun(s.Kind)
	if count == 0 {
		if !target.SignatureSpecified {
			return sqlError("42883", fmt.Sprintf(`could not find a %s named "%s"`, noun, display))
		}
		return sqlError("42883", noun+" "+display+"("+canonical+") does not exist")
	}
	if count > 1 {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42725", Message: fmt.Sprintf(`%s name "%s" is not unique`, s.Kind, display), Hint: "Specify the argument list to select the " + s.Kind + " unambiguously."}
	}
	if s.Kind == "procedure" && d.RoutineKind != catalog.ProcedureRoutine || s.Kind == "function" && d.RoutineKind == catalog.ProcedureRoutine {
		return sqlError("42809", display+"("+canonical+") is not a "+noun)
	}
	next := *d
	view := h.AccessView(auth)
	if s.Action == "owner" {
		owner, err := access.SchemaOwner(view, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
		if owner == d.Owner {
			return nil
		}
		next.Owner = owner
	}
	if s.Action == "schema" {
		var ns ids.ObjectRef
		ns, auth, err = h.CreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		if err = h.CheckCreateIn(auth, ns); err != nil {
			return err
		}
		if ns.Address == d.Namespace() {
			return nil
		}
		view = h.AccessView(auth)
		source, destination := view.Catalog.MustNamespace(d.Namespace()), view.Catalog.MustNamespace(ns.Address)
		if source.Temporary != (ids.SessionToken{}) || destination.Temporary != (ids.SessionToken{}) {
			return sqlError("0A000", "cannot move objects into or out of temporary schemas")
		}
		if source.Address().ObjectID == ids.PGToastNamespaceOID || destination.Address().ObjectID == ids.PGToastNamespaceOID {
			return sqlError("0A000", "cannot move objects into or out of TOAST schema")
		}
		next.ObjectNamespace = ns.Address
	}
	if err = ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, d.Address()), OwnerOnly: true}); err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
			kind := "function"
			if d.RoutineKind == catalog.ProcedureRoutine {
				kind = "procedure"
			} else if d.RoutineKind == catalog.AggregateRoutine {
				kind = "aggregate"
			}
			return sqlError("42501", "must be owner of "+kind+" "+d.Name())
		}
		return err
	}
	switch s.Action {
	case "rename":
		if err = h.CheckCreateIn(auth, ddl.ObjectRef(h, auth, d.Namespace())); err != nil {
			return err
		}
		next.ObjectName = s.Target.Text
	case "owner":
		if err = access.OwnershipEligibility(view, auth, next.Owner, d.Namespace()); err != nil {
			return err
		}
		next.ACL = authorization.SchemaACLOwner(d.ACL, d.Owner, next.Owner)
	}
	if s.Action != "owner" {
		if address := h.Stage().LookupName(catalog.DescriptorNameKey(&next)); address != (ids.ObjectAddress{}) {
			if !target.SignatureSpecified && d.Signature != "" {
				canonical, err = utilityRoutineSignatureDiagnostic(h, auth, d.Signature)
				if err != nil {
					return err
				}
			}
			return sqlError("42723", fmt.Sprintf(`function %s(%s) already exists in schema "%s"`, catalog.DependencyIdentifier(next.Name()), canonical, view.Catalog.MustNamespace(next.Namespace()).Name()))
		}
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if err = h.Stage().Replace(&next); err != nil {
		return err
	}
	return h.RecordDependencyPath(auth)
}
