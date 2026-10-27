package extension

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CitextStringMember is one immutable slot in the released scalar string roster.
// ThirdCitext distinguishes the installed scalar from a builtin third formal.
type CitextStringMember struct {
	Name        string
	Third       sqltype.ID
	ThirdCitext bool
	Result      ids.OID
}

var CitextStringMembers = [...]CitextStringMember{
	{"strpos", 0, false, 23}, {"replace", 0, true, 25}, {"split_part", sqltype.Int4, false, 25}, {"translate", sqltype.Text, false, 25},
}

func CitextStringName(name string) bool {
	for _, m := range CitextStringMembers {
		if m.Name == name {
			return true
		}
	}
	return false
}
func (m CitextStringMember) ArgCount() int {
	if m.Third == 0 && !m.ThirdCitext {
		return 2
	}
	return 3
}
func (m CitextStringMember) Signature(id sqltype.ID) catalog.ProcedureSignature {
	s := string(CitextComparisonSignature(id))
	if m.ArgCount() == 3 {
		third := m.Third
		if m.ThirdCitext {
			third = id
		}
		s += "," + string(CitextSignature(third))
	}
	return catalog.ProcedureSignature(s)
}
func (m CitextStringMember) OwnerSignature() string {
	s := "citext,citext"
	if m.ThirdCitext {
		return s + ",citext"
	}
	if m.Third != 0 {
		s += "," + string(CitextSignature(m.Third))
	}
	return s
}
func (m CitextStringMember) Declaration() catalog.InstalledRoutineDeclaration {
	return catalog.InstalledRoutineDeclaration{Parallel: 's'}
}
func (p *CitextProvider) stageStrings(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, typ *sqltype.Descriptor) error {
	for i, m := range CitextStringMembers {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		routine := &catalog.Procedure{ObjectAddress: ext.Members[CitextStringProcedureStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: m.Name, Signature: m.Signature(typ.ID()), RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: m.Result, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.SQLRoutineLanguage}, Installed: m.Declaration(), Implementation: p}
		routine.DeclarationBody = citextStringDeclarationBodies[i]
		if stage.LookupName(catalog.DescriptorNameKey(routine)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, m.Name)}
		}
		if err := stage.Create(routine); err != nil {
			return err
		}
	}
	return nil
}
