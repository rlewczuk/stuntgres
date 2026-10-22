package index

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func StageCreateAccessMethod(h FamilyDDLHost, auth ddl.Authority, s *parser.CreateAccessMethod) error {
	v := h.AccessView(auth)
	actor, err := v.Roles.Descriptor(v.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`permission denied to create access method "%s"`, s.Name.Text), Hint: `Must be superuser to create an access method.`}
	}
	if _, ok := h.StageRead().LookupAccessMethod(s.Name.Text); ok {
		return familyError("42710", fmt.Sprintf(`access method "%s" already exists`, s.Name.Text))
	}
	signature := catalog.ProcedureSignature("2281")
	identity, count, err := h.SelectFamilyFunction(auth, s.Handler, &signature)
	if err != nil {
		return err
	}
	routine, installed := v.Catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: identity.ID})
	if count != 1 || installed && routine.RoutineKind != catalog.FunctionRoutine {
		return familyError("42883", fmt.Sprintf(`function %s(internal) does not exist`, catalog.ObjectNameText(s.Handler)))
	}
	result, known := catalog.BuiltinRoutineResultByOID(identity.ID)
	if installed {
		result, known = routine.Result, true
	}
	expected := ids.OID(325)
	resultName := "index_am_handler"
	if s.MethodKind == "table" {
		expected, resultName = 269, "table_am_handler"
	}
	if !known || result != expected {
		return familyError("42809", fmt.Sprintf(`function %s must return type %s`, identity.Name, resultName))
	}
	if !installed || routine.AccessMethod == catalog.NoAccessMethod || catalog.NativeHandler(identity.ID) != routine.AccessMethod {
		return familyError("0A000", `access method handler has no registered native implementation`)
	}
	oid, err := h.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := h.Revision()
	if err != nil {
		return err
	}
	return h.Stage().Create(&catalog.AccessMethod{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: oid}, ObjectRevision: revision, ObjectName: s.Name.Text, Handler: identity.ID, Native: routine.AccessMethod})
}
func StageDropAccessMethod(h FamilyDDLHost, auth ddl.Authority, s *parser.DropAccessMethod) error {
	method, ok := h.StageRead().LookupAccessMethod(s.Name.Text)
	if !ok {
		message := fmt.Sprintf(`access method "%s" does not exist`, s.Name.Text)
		if s.IfExists {
			h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message + ", skipping"})
			return nil
		}
		return familyError("42704", message)
	}
	v := h.AccessView(auth)
	actor, err := v.Roles.Descriptor(v.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return familyError("42501", "must be superuser")
	}
	if catalog.AccessMethodSeeded(method) {
		return familyError("2BP01", "cannot drop access method "+catalog.DependencyIdentifier(method.Name())+" because it is required by the database system")
	}
	h.DropTarget(method.Address())
	return h.RecordDependencyPath(auth)
}

// Canonical logical SQL names the destination's registered handler, never a
// source capability object or numeric user identity. Global methods have no owner.
func AccessMethodSQL(view catalog.Read, m *catalog.AccessMethod) (string, error) {
	p, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: m.Handler})
	if !ok || p.AccessMethod != m.Native {
		return "", fmt.Errorf("stuntgres: missing access method handler capability")
	}
	ns, ok := view.Namespace(p.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing handler namespace")
	}
	kind := "INDEX"
	if m.MethodKind() == "t" {
		kind = "TABLE"
	}
	return "CREATE ACCESS METHOD " + catalog.QuoteIdentifier(m.Name()) + " TYPE " + kind + " HANDLER " + catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(p.Name()) + ";", nil
}
