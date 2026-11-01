package extension

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (p *IsnProvider) stageDeclarations(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	types := stage.CurrentRead()
	for slot := IsnComparisonStart; slot < IsnFamilySlot; slot++ {
		if isnMemberClass(slot) != ids.ProcedureClassID {
			continue
		}
		name, _, signature, result, display, err := isnRoutineDeclaration(types, ext.Members, slot)
		if err != nil {
			return err
		}
		rev, err := stage.Revision()
		if err != nil {
			return err
		}
		if err := stage.Create(&catalog.Procedure{ObjectAddress: ext.Members[slot], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: expr.Immutable, Strict: true, Display: display, Installed: catalog.InstalledRoutineDeclaration{Parallel: 's'}, Implementation: p}); err != nil {
			return err
		}
	}
	for i, pair := range isnPairs {
		for op, name := range cubeOperatorNames {
			rev, err := stage.Revision()
			if err != nil {
				return err
			}
			d := &catalog.Operator{ObjectAddress: ext.Members[IsnOperatorStart+i*6+op], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: name, Owner: owner, Left: ext.Members[isnRecipes[pair[0]].slot].ObjectID, Right: ext.Members[isnRecipes[pair[1]].slot].ObjectID, Result: 16, Function: ext.Members[IsnComparisonStart+i*6+op].ObjectID, Commutator: ext.Members[IsnOperatorStart+isnPairIndex(pair[1], pair[0])*6+cubeCommutators[op]].ObjectID, Negator: ext.Members[IsnOperatorStart+i*6+cubeNegators[op]].ObjectID, Restriction: cubeRestrictions[op], Join: cubeJoins[op], Hash: op == 0, Merge: op == 0}
			if err := stage.Create(d); err != nil {
				return err
			}
		}
	}
	rev, err := stage.Revision()
	if err != nil {
		return err
	}
	if err := stage.Create(&catalog.OperatorFamily{ObjectAddress: ext.Members[IsnFamilySlot], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "isn_ops", Method: 403, Owner: owner}); err != nil {
		return err
	}
	for role, recipe := range isnRecipes {
		rev, err := stage.Revision()
		if err != nil {
			return err
		}
		class := &catalog.OperatorClass{ObjectAddress: ext.Members[IsnClassStart+role], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: recipe.name + "_ops", Owner: owner, Method: 403, Family: ext.Members[IsnFamilySlot].ObjectID, InputType: ext.Members[recipe.slot].ObjectID, Default: true}
		if err := stage.Create(class); err != nil {
			return err
		}
		for i, op := range cubeStrategies {
			oid, err := stage.AllocateOID()
			if err != nil {
				return err
			}
			rev, err := stage.Revision()
			if err != nil {
				return err
			}
			if err := stage.Create(&catalog.OperatorFamilyOperator{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: oid}, ObjectRevision: rev, Family: class.Family, Left: class.InputType, Right: class.InputType, Operator: ext.Members[IsnOperatorStart+isnPairIndex(role, role)*6+op].ObjectID, Method: 403, InternalOwner: class.Address(), Number: int16(i + 1), Purpose: 's'}); err != nil {
				return err
			}
		}
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		rev, err = stage.Revision()
		if err != nil {
			return err
		}
		if err := stage.Create(&catalog.OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: oid}, ObjectRevision: rev, Family: class.Family, Left: class.InputType, Right: class.InputType, Function: ext.Members[IsnSupportStart+role].ObjectID, InternalOwner: class.Address(), Number: 1, CatalogVisible: true}); err != nil {
			return err
		}
	}
	for i, pair := range isnCastPairs {
		rev, err := stage.Revision()
		if err != nil {
			return err
		}
		d := &catalog.Cast{OID: ext.Members[IsnCastStart+i].ObjectID, ObjectRevision: rev, Source: sqltype.ID(ext.Members[isnRecipes[pair[0]].slot].ObjectID), Target: sqltype.ID(ext.Members[isnRecipes[pair[1]].slot].ObjectID), Context: "a", Method: "b", Extension: ext.Address()}
		if pair[0] == 0 {
			d.Context, d.Method, d.Function = "e", "f", ext.Members[IsnConversionStart+pair[1]-1]
		}
		if err := stage.Create(d); err != nil {
			return err
		}
	}
	return nil
}
