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

// CitextRegexpMember describes one slot in the versioned installation roster.
type CitextRegexpMember struct {
	Name        string
	ArgCount    int
	Result      ids.OID
	Declaration catalog.InstalledRoutineDeclaration
}

func citextRegexpMember(name string, n int, result ids.OID, set bool, rows float32) CitextRegexpMember {
	names := [4]string{"string", "pattern"}
	if name == "regexp_replace" {
		names[2] = "replacement"
		if n == 4 {
			names[3] = "flags"
		}
	} else if n == 3 {
		names[2] = "flags"
	}
	return CitextRegexpMember{name, n, result, catalog.InstalledRoutineDeclaration{
		ArgumentNames: names, Parallel: 's', ReturnsSet: set, Rows: rows,
	}}
}

// CitextRegexpMembers retains the declaration order of citext 1.8.
var CitextRegexpMembers = [...]CitextRegexpMember{
	citextRegexpMember("regexp_match", 2, 1009, false, 0),
	citextRegexpMember("regexp_match", 3, 1009, false, 0),
	citextRegexpMember("regexp_matches", 2, 1009, true, 1),
	citextRegexpMember("regexp_matches", 3, 1009, true, 10),
	citextRegexpMember("regexp_replace", 3, 25, false, 0),
	citextRegexpMember("regexp_replace", 4, 25, false, 0),
	citextRegexpMember("regexp_split_to_array", 2, 1009, false, 0),
	citextRegexpMember("regexp_split_to_array", 3, 1009, false, 0),
	citextRegexpMember("regexp_split_to_table", 2, 25, true, 1000),
	citextRegexpMember("regexp_split_to_table", 3, 25, true, 1000),
}

func CitextRegexpName(name string) bool {
	for _, member := range CitextRegexpMembers {
		if member.Name == name {
			return true
		}
	}
	return false
}

func (m CitextRegexpMember) Signature(id sqltype.ID) catalog.ProcedureSignature {
	signature := string(CitextComparisonSignature(id))
	for i := 2; i < m.ArgCount; i++ {
		signature += ",25"
	}
	return catalog.ProcedureSignature(signature)
}

func (p *CitextProvider) stageRegexp(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, typ *sqltype.Descriptor) error {
	for i, member := range CitextRegexpMembers {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		routine := &catalog.Procedure{
			ObjectAddress: ext.Members[CitextRegexpProcedureStart+i], ObjectNamespace: ext.TargetNamespace,
			Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name,
			Signature: member.Signature(typ.ID()), RoutineKind: catalog.FunctionRoutine,
			Owner: owner, Result: member.Result, Volatility: expr.Immutable, Strict: true,
			Display: catalog.RoutineDisplay{Language: catalog.SQLRoutineLanguage},
			Installed: member.Declaration, Implementation: p,
		}
		if stage.LookupName(catalog.DescriptorNameKey(routine)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723",
				Message: fmt.Sprintf(`function "%s" already exists with same argument types`, member.Name),
			}
		}
		if err := stage.Create(routine); err != nil {
			return err
		}
	}
	return nil
}
