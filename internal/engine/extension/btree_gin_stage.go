package extension

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

func (p *BtreeGinProvider) Stage(stage Stage, e *catalog.Extension, owner authorization.PrincipalRef) error {
	e.Members = make([]ids.ObjectAddress, btreeGinMemberCount)
	for slot := range e.Members {
		class := ids.ProcedureClassID
		if slot >= btreeGinRoutineCount {
			class = ids.OperatorFamilyClassID
			if (slot-btreeGinRoutineCount)%2 == 1 {
				class = ids.OperatorClassCatalogID
			}
		}
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		e.Members[slot] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	for slot, spec := range btreeGinRoutines {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		procedure := &catalog.Procedure{
			ObjectAddress: e.Members[slot], ObjectNamespace: e.TargetNamespace, ObjectRevision: revision, ObjectName: spec.name,
			Extension: e.Address(), Owner: owner, Signature: spec.signature, Result: spec.result,
			RoutineKind: catalog.FunctionRoutine, Volatility: expr.Immutable, Strict: true, Implementation: p,
			Installed: catalog.InstalledRoutineDeclaration{Parallel: 'u'},
			Display:   catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: spec.name, Binary: "$libdir/btree_gin", HasBinary: true},
		}
		if stage.LookupName(catalog.DescriptorNameKey(procedure)) != (ids.ObjectAddress{}) {
			return MemberConflict(procedure, e.TargetNamespace)
		}
		if err := stage.Create(procedure); err != nil {
			return err
		}
	}
	for column, spec := range btreeGinClasses {
		familySlot := btreeGinRoutineCount + column*2
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		family := &catalog.OperatorFamily{ObjectAddress: e.Members[familySlot], ObjectNamespace: e.TargetNamespace, ObjectRevision: revision, ObjectName: spec.name, Extension: e.Address(), Owner: owner, Method: 2742}
		if err := stage.Create(family); err != nil {
			return err
		}
		revision, err = stage.Revision()
		if err != nil {
			return err
		}
		class := &catalog.OperatorClass{ObjectAddress: e.Members[familySlot+1], ObjectNamespace: e.TargetNamespace, ObjectRevision: revision, ObjectName: spec.name, Extension: e.Address(), Owner: owner, Method: 2742, Family: family.Address().ObjectID, InputType: spec.input, Default: true}
		if err := stage.Create(class); err != nil {
			return err
		}
		routines := spec.supportRoutines(e.Members)
		for number, operator := range spec.operators {
			oid, err := stage.AllocateOID()
			if err != nil {
				return err
			}
			revision, err := stage.Revision()
			if err != nil {
				return err
			}
			if err := stage.Create(&catalog.OperatorFamilyOperator{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: oid}, ObjectRevision: revision, Family: class.Family, Left: spec.search, Right: spec.search, Operator: operator, Method: class.Method, InternalOwner: spec.memberOwner(class.Address(), number+1, false), Number: int16(number + 1), Purpose: 's'}); err != nil {
				return err
			}
			oid, err = stage.AllocateOID()
			if err != nil {
				return err
			}
			revision, err = stage.Revision()
			if err != nil {
				return err
			}
			if err := stage.Create(&catalog.OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: oid}, ObjectRevision: revision, Family: class.Family, Left: spec.input, Right: spec.input, Function: routines[number], InternalOwner: spec.memberOwner(class.Address(), number+1, true), Number: int16(number + 1), CatalogVisible: true}); err != nil {
				return err
			}
		}
	}
	return nil
}
