package index

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func findOperatorClass(h FamilyDDLHost, auth ddl.Authority, name parser.ObjectName, method ids.OID) (*catalog.OperatorClass, error) {
	if len(name.Parts) == 2 {
		parts := [2]parser.Identifier{name.Parts[0], name.Parts[1]}
		parts[0].Pos = parser.Position{}
		name.Parts = parts[:]
	}
	path, err := h.SearchNamespaces(auth, name, ids.OperatorClassCatalogID)
	if err != nil {
		return nil, err
	}
	for _, ns := range path {
		if d, ok := h.AccessView(auth).Catalog.LookupOperatorClass(ns, method, name.Parts[len(name.Parts)-1].Text); ok {
			return d, nil
		}
	}
	return nil, familyError("42704", fmt.Sprintf(`operator class "%s" does not exist for access method "%s"`, catalog.ObjectNameText(name), familyMethodName(h.AccessView(auth).Catalog, method)))
}
func classOwner(h FamilyDDLHost, auth ddl.Authority, d *catalog.OperatorClass) error {
	if err := ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, d.Address()), OwnerOnly: true}); err != nil {
		return familyError("42501", fmt.Sprintf("must be owner of operator class %s", d.Name()))
	}
	return nil
}
func StageAlterOperatorClass(h FamilyDDLHost, auth ddl.Authority, s *parser.AlterOperatorClass) error {
	method, err := familyMethod(h.StageRead(), s.Method.Text)
	if err != nil {
		return err
	}
	d, err := findOperatorClass(h, auth, s.Name, method)
	if err != nil {
		return err
	}
	if s.Action != "schema" {
		if err = classOwner(h, auth, d); err != nil {
			return err
		}
	}
	next := *d
	switch s.Action {
	case "rename":
		// Released rename requires CREATE in the current namespace as well as ownership.
		if err = h.CheckCreateIn(auth, ddl.ObjectRef(h, auth, d.Namespace())); err != nil {
			return err
		}
		next.ObjectName = s.Target.Text
	case "owner":
		v := h.AccessView(auth)
		owner, e := access.SchemaOwner(v, s.Target.Text, s.OwnerIdentity)
		if e != nil {
			return e
		}
		if err = access.OwnershipEligibility(v, auth, owner, d.Namespace()); err != nil {
			return err
		}
		next.Owner = owner
	case "schema":
		ns, a, e := h.CreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if e != nil {
			return e
		}
		auth = a
		if err = h.CheckCreateIn(auth, ns); err != nil {
			return err
		}
		if err = classOwner(h, auth, d); err != nil {
			return err
		}
		next.ObjectNamespace = ns.Address
	}
	if a := h.Stage().LookupName(catalog.DescriptorNameKey(&next)); a != (ids.ObjectAddress{}) && a != d.Address() {
		ns := h.StageRead().MustNamespace(next.Namespace())
		return familyError("42710", fmt.Sprintf(`operator class "%s" for access method "%s" already exists in schema "%s"`, next.Name(), s.Method.Text, ns.Name()))
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	return ddl.ReplaceCommand(h.Stage(), "ALTER OPERATOR CLASS", &next)
}
func StageDropOperatorClass(h FamilyDDLHost, auth ddl.Authority, s *parser.DropOperatorClass) error {
	method, err := familyMethod(h.StageRead(), s.Method.Text)
	if err != nil {
		return err
	}
	d, err := findOperatorClass(h, auth, s.Name, method)
	if err != nil {
		if diag, ok := err.(*sqlstate.Error); ok && s.IfExists && (diag.Code == "42704" || diag.Code == "3F000") {
			h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: diag.Message + ", skipping"})
			return nil
		}
		return err
	}
	if err = classOwner(h, auth, d); err != nil {
		return familyError("42501", "must be owner of operator class "+s.Method.Text+"."+catalog.ObjectNameText(s.Name))
	}
	if catalog.OperatorClassSeeded(d) {
		object, _ := h.DependencyObject(auth, d.Address())
		return familyError("2BP01", "cannot drop "+object+" because it is required by the database system")
	}
	h.DropTarget(d.Address())
	return h.RecordDependencyPath(auth)
}
