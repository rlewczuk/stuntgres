package extension

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (p *CubeProvider) stageComparisons(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, typ *sqltype.Descriptor) error {
	for i, name := range cubeOperatorNames {
		rev, err := stage.Revision()
		if err != nil {
			return err
		}
		o := &catalog.Operator{ObjectAddress: ext.Members[10+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: name, Owner: owner, Left: ids.OID(typ.ID()), Right: ids.OID(typ.ID()), Result: 16, Function: ext.Members[i+3].ObjectID, Commutator: ext.Members[10+cubeCommutators[i]].ObjectID, Negator: ext.Members[10+cubeNegators[i]].ObjectID, Restriction: cubeRestrictions[i], Join: cubeJoins[i], Merge: i == 0}
		if err := stage.Create(o); err != nil {
			return err
		}
	}
	rev, err := stage.Revision()
	if err != nil {
		return err
	}
	family := &catalog.OperatorFamily{ObjectAddress: ext.Members[16], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "cube_ops", Method: 403, Owner: owner}
	if err := stage.Create(family); err != nil {
		return err
	}
	rev, err = stage.Revision()
	if err != nil {
		return err
	}
	class := &catalog.OperatorClass{ObjectAddress: ext.Members[17], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "cube_ops", Owner: owner, Method: 403, Family: family.Address().ObjectID, InputType: ids.OID(typ.ID()), Default: true}
	if err := stage.Create(class); err != nil {
		return err
	}
	for i, operator := range cubeStrategies {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		rev, err := stage.Revision()
		if err != nil {
			return err
		}
		row := &catalog.OperatorFamilyOperator{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: oid}, ObjectRevision: rev, Family: class.Family, Left: class.InputType, Right: class.InputType, Operator: ext.Members[10+operator].ObjectID, Method: 403, InternalOwner: class.Address(), Number: int16(i + 1), Purpose: 's'}
		if err := stage.Create(row); err != nil {
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
	return stage.Create(&catalog.OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: oid}, ObjectRevision: rev, Family: class.Family, Left: class.InputType, Right: class.InputType, Function: ext.Members[9].ObjectID, InternalOwner: class.Address(), Number: 1})
}
