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

const CitextComparisonProcedureStart = 3
const CitextComparisonOperatorStart = 9
const CitextConversionProcedureStart = 15
const CitextPatternProcedureStart = 18
const CitextPatternOperatorStart = 26
const CitextMemberCount = 42

// The slot order is the immutable installed manifest, not a public name lookup.
var CitextComparisonNames = [...]string{"citext_eq", "citext_ne", "citext_lt", "citext_le", "citext_gt", "citext_ge"}
var citextOperatorNames = [...]string{"=", "<>", "<", "<=", ">", ">="}

func CitextComparisonName(name string) bool {
	for _, candidate := range CitextComparisonNames {
		if candidate == name {
			return true
		}
	}
	return false
}
func CitextComparisonSignature(id sqltype.ID) catalog.ProcedureSignature {
	s := string(CitextSignature(id))
	return catalog.ProcedureSignature(s + "," + s)
}

func (p *CitextProvider) stageComparisons(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, typ *sqltype.Descriptor) error {
	for i, name := range CitextComparisonNames {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		proc := &catalog.Procedure{ObjectAddress: ext.Members[CitextComparisonProcedureStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: CitextComparisonSignature(typ.ID()), RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: ids.OID(sqltype.Bool), Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	for i, name := range citextOperatorNames {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		op := &catalog.Operator{ObjectAddress: ext.Members[CitextComparisonOperatorStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Owner: owner, Left: ids.OID(typ.ID()), Right: ids.OID(typ.ID()), Result: ids.OID(sqltype.Bool), Function: ext.Members[CitextComparisonProcedureStart+i].ObjectID, Commutator: ext.Members[CitextComparisonOperatorStart+cubeCommutators[i]].ObjectID, Negator: ext.Members[CitextComparisonOperatorStart+cubeNegators[i]].ObjectID, Restriction: cubeRestrictions[i], Join: cubeJoins[i]}
		if err := stage.Create(op); err != nil {
			return err
		}
	}
	return nil
}

func validateCitextOperator(v catalog.Read, ext *catalog.Extension, base *catalog.Type, i int) error {
	op, ok := v.Operator(ext.Members[CitextComparisonOperatorStart+i])
	if !ok || op.Address() != ext.Members[CitextComparisonOperatorStart+i] || op.Namespace() != ext.TargetNamespace || op.Name() != citextOperatorNames[i] || op.Extension != ext.Address() || op.Left != ids.OID(base.SQLType.ID()) || op.Right != op.Left || op.Result != ids.OID(sqltype.Bool) || op.Function != ext.Members[CitextComparisonProcedureStart+i].ObjectID || op.Commutator != ext.Members[CitextComparisonOperatorStart+cubeCommutators[i]].ObjectID || op.Negator != ext.Members[CitextComparisonOperatorStart+cubeNegators[i]].ObjectID || op.Restriction != cubeRestrictions[i] || op.Join != cubeJoins[i] || op.Hash || op.Merge {
		return fmt.Errorf("stuntgres: invalid citext operator")
	}
	return ValidateProviderHeader(v, op, ids.OperatorClassID)
}
