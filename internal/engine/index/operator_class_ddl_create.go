package index

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func StageCreateOperatorClass(h FamilyDDLHost, auth ddl.Authority, s *parser.CreateOperatorClass) error {
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
	v := h.AccessView(auth)
	actor, err := v.Roles.Descriptor(v.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return familyError("42501", "must be superuser to create an operator class")
	}
	input, err := h.ResolveRegtype(auth, s.InputType)
	if err != nil {
		return err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	var family *catalog.OperatorFamily
	if len(s.Family.Parts) != 0 {
		family, err = findFamily(h, auth, s.Family, method)
		if err != nil {
			return err
		}
	} else {
		family, _ = v.Catalog.OperatorFamily(v.Catalog.NameAddress(catalog.FamilyNameKey(ns.Address, method, name)))
		if family == nil {
			oid, e := h.AllocateOID()
			if e != nil {
				return e
			}
			revision, e := h.Revision()
			if e != nil {
				return e
			}
			family = &catalog.OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: oid}, ObjectNamespace: ns.Address, ObjectRevision: revision, ObjectName: name, Method: method, Owner: v.State.CurrentUser}
			if err = ddl.CreateCommand(h.Stage(), "CREATE OPERATOR FAMILY", family); err != nil {
				return err
			}
		}
	}
	class := &catalog.OperatorClass{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID}, ObjectNamespace: ns.Address, ObjectName: name, Method: method, Family: family.Address().ObjectID, InputType: input, Default: s.Default, Owner: v.State.CurrentUser}
	beforeStore := func() error {
		if h.Stage().LookupName(catalog.ClassNameKey(ns.Address, method, name)) != (ids.ObjectAddress{}) {
			return familyError("42710", fmt.Sprintf(`operator class "%s" for access method "%s" already exists`, name, s.Method.Text))
		}
		if s.Default {
			if old, ok := v.Catalog.AnyDefaultOperatorClass(method, input); ok {
				typeName := familyTypeNames(v.Catalog, []ids.OID{input}, "")
				if typ, found := v.Catalog.LookupType(sqltype.ID(input)); found {
					typeName = typ.SQLName()
					if !typ.IsUserDefined() {
						typeName = "pg_catalog." + typ.Name()
					}
				}
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: fmt.Sprintf(`could not make operator class "%s" be default for type %s`, name, typeName), Detail: fmt.Sprintf(`Operator class "%s" already is the default.`, old.Name())}
			}
		}
		class.ObjectAddress.ObjectID, err = h.AllocateOID()
		if err != nil {
			return err
		}
		class.ObjectRevision, err = h.Revision()
		if err != nil {
			return err
		}
		return ddl.CreateCommand(h.Stage(), "CREATE OPERATOR CLASS", class)
	}
	return stageOperatorMembers(h, auth, family, &parser.AlterOperatorFamily{Name: s.Name, Method: s.Method, Action: "add", Items: s.Items}, class, beforeStore)
}

func classMemberCollision(d catalog.Descriptor) error {
	var table, constraint, detail string
	switch m := d.(type) {
	case *catalog.OperatorFamilySupport:
		table, constraint = "pg_amproc", "pg_amproc_fam_proc_index"
		detail = fmt.Sprintf("Key (amprocfamily, amproclefttype, amprocrighttype, amprocnum)=(%d, %d, %d, %d) already exists.", m.Family, m.Left, m.Right, m.Number)
	case *catalog.OperatorFamilyOperator:
		table, constraint = "pg_amop", "pg_amop_fam_strat_index"
		detail = fmt.Sprintf("Key (amopfamily, amoplefttype, amoprighttype, amopstrategy)=(%d, %d, %d, %d) already exists.", m.Family, m.Left, m.Right, m.Number)
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: fmt.Sprintf(`duplicate key value violates unique constraint "%s"`, constraint), Detail: detail, Schema: "pg_catalog", Table: table, Constraint: constraint}
}
