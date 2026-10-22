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

type FamilyDDLHost interface {
	ddl.Host
	SelectFamilyFunction(ddl.Authority, parser.ObjectName, *catalog.ProcedureSignature) (catalog.RoutineIdentity, int, error)
	ResolveRegtype(ddl.Authority, parser.TypeName) (ids.OID, error)
	// CheckFamilyMemberDrop checks one resolved target's RESTRICT eligibility
	// before a later deletion target can report a missing-member error.
	CheckFamilyMemberDrop(ddl.Authority, ids.ObjectAddress) error
}

func familyError(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func familyMethod(view catalog.Read, name string) (ids.OID, error) {
	if method, ok := view.LookupAccessMethod(name); ok {
		if method.MethodKind() != "i" {
			return 0, familyError("55000", fmt.Sprintf(`access method "%s" is not of type INDEX`, name))
		}
		return method.Address().ObjectID, nil
	}
	return 0, familyError("42704", fmt.Sprintf(`access method "%s" does not exist`, name))
}
func familyMethodName(view catalog.Read, method ids.OID) string {
	if fact, ok := view.AccessMethod(method); ok {
		return fact.Name()
	}
	return ""
}
func familySuperuser(h FamilyDDLHost, auth ddl.Authority, verb string) error {
	v := h.AccessView(auth)
	actor, err := v.Roles.Descriptor(v.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return familyError("42501", "must be superuser to "+verb+" an operator family")
	}
	return nil
}
func findFamily(h FamilyDDLHost, auth ddl.Authority, name parser.ObjectName, method ids.OID) (*catalog.OperatorFamily, error) {
	// Object-address utility lookup has no expression location. Keep the
	// shared namespace authority, without changing positions for other callers.
	if len(name.Parts) == 2 {
		parts := [2]parser.Identifier{name.Parts[0], name.Parts[1]}
		parts[0].Pos = parser.Position{}
		name.Parts = parts[:]
	}
	path, err := h.SearchNamespaces(auth, name, ids.OperatorFamilyClassID)
	if err != nil {
		return nil, err
	}
	v := h.AccessView(auth).Catalog
	for _, ns := range path {
		a := v.NameAddress(catalog.FamilyNameKey(ns, method, name.Parts[len(name.Parts)-1].Text))
		if d, ok := v.OperatorFamily(a); ok {
			return d, nil
		}
	}
	return nil, familyError("42704", fmt.Sprintf(`operator family "%s" does not exist for access method "%s"`, catalog.ObjectNameText(name), familyMethodName(v, method)))
}
func familyOwner(h FamilyDDLHost, auth ddl.Authority, d *catalog.OperatorFamily) error {
	if err := ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, d.Address()), OwnerOnly: true}); err != nil {
		return familyError("42501", fmt.Sprintf(`must be owner of operator family %s`, d.Name()))
	}
	return nil
}
func StageCreateOperatorFamily(h FamilyDDLHost, auth ddl.Authority, s *parser.CreateOperatorFamily) error {
	ns, auth, err := h.CreationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	if err = h.CheckCreateIn(auth, ns); err != nil {
		return err
	}
	method, err := familyMethod(h.StageRead(), s.Method.Text)
	if err != nil {
		return err
	}
	if err = familySuperuser(h, auth, "create"); err != nil {
		return err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	if h.Stage().LookupName(catalog.FamilyNameKey(ns.Address, method, name)) != (ids.ObjectAddress{}) {
		return familyError("42710", fmt.Sprintf(`operator family "%s" for access method "%s" already exists`, name, s.Method.Text))
	}
	oid, err := h.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := h.Revision()
	if err != nil {
		return err
	}
	return h.Stage().Create(&catalog.OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: oid}, ObjectNamespace: ns.Address, ObjectRevision: revision, ObjectName: name, Method: method, Owner: h.AccessView(auth).State.CurrentUser})
}
func StageAlterOperatorFamily(h FamilyDDLHost, auth ddl.Authority, s *parser.AlterOperatorFamily) error {
	method, err := familyMethod(h.StageRead(), s.Method.Text)
	if err != nil {
		return err
	}
	d, err := findFamily(h, auth, s.Name, method)
	if err != nil {
		return err
	}
	if s.Action == "add" || s.Action == "drop" {
		if err = familySuperuser(h, auth, "alter"); err != nil {
			return err
		}
		return stageFamilyMembers(h, auth, d, s)
	}
	if err = familyOwner(h, auth, d); err != nil {
		return err
	}
	next := *d
	switch s.Action {
	case "rename":
		next.ObjectName = s.Target.Text
	case "owner":
		v := h.AccessView(auth)
		owner, err := access.SchemaOwner(v, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
		if err = access.OwnershipEligibility(v, auth, owner, d.Namespace()); err != nil {
			return err
		}
		next.Owner = owner
	case "schema":
		ns, a, err := h.CreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		auth = a
		if err = h.CheckCreateIn(auth, ns); err != nil {
			return err
		}
		next.ObjectNamespace = ns.Address
	}
	if a := h.Stage().LookupName(catalog.DescriptorNameKey(&next)); a != (ids.ObjectAddress{}) && a != d.Address() {
		return familyError("42710", fmt.Sprintf(`operator family "%s" for access method "%s" already exists`, next.Name(), s.Method.Text))
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	return h.Stage().Replace(&next)
}
func StageDropOperatorFamily(h FamilyDDLHost, auth ddl.Authority, s *parser.DropOperatorFamily) error {
	method, err := familyMethod(h.StageRead(), s.Method.Text)
	if err != nil {
		return err
	}
	d, err := findFamily(h, auth, s.Name, method)
	if err != nil {
		if diag, ok := err.(*sqlstate.Error); ok && s.IfExists && (diag.Code == "42704" || diag.Code == "3F000") {
			h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: diag.Message + ", skipping"})
			return nil
		}
		return err
	}
	if err = familyOwner(h, auth, d); err != nil {
		return familyError("42501", "must be owner of operator family "+s.Method.Text+"."+catalog.ObjectNameText(s.Name))
	}
	if catalog.OperatorFamilySeeded(d) {
		object, _ := h.DependencyObject(auth, d.Address())
		return familyError("2BP01", "cannot drop "+object+" because it is required by the database system")
	}
	h.DropTarget(d.Address())
	_ = h.RecordDependencyPath(auth)
	return nil
}
