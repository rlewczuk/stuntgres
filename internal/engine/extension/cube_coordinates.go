package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var cubeCoordinateOperators = [...]string{"->", "~>"}

func cubeCoordinateOwnerName(name string) bool {
	return name == "->" || name == "~>"
}

func (p *CubeProvider) stageCoordinates(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, typ *sqltype.Descriptor) error {
	for i, name := range cubeCoordinateOperators {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		// The last two routine declarations are the genuine operator backings.
		proc := ext.Members[cubeRoutineMember(len(cubeRoutines)-2+i)]
		op := &catalog.Operator{ObjectAddress: ext.Members[cubeCoordinateOperatorStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Left: ids.OID(typ.ID()), Right: ids.OID(sqltype.Int4), Owner: owner, Result: ids.OID(sqltype.Float8), Function: proc.ObjectID}
		if stage.LookupName(catalog.DescriptorNameKey(op)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: fmt.Sprintf("operator %s already exists", name)}
		}
		if err := stage.Create(op); err != nil {
			return err
		}
	}
	return nil
}

func (p *CubeProvider) validateCoordinates(v catalog.Read, ext *catalog.Extension, base *catalog.Type) error {
	for i, name := range cubeCoordinateOperators {
		address := ext.Members[cubeCoordinateOperatorStart+i]
		op, ok := v.Operator(address)
		if !ok || address.ClassID != ids.OperatorClassID || op.Address() != address || op.Namespace() != ext.TargetNamespace || op.Extension != ext.Address() || op.Revision() == 0 || op.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(op)) != address || op.Name() != name || op.Left != ids.OID(base.SQLType.ID()) || op.Right != ids.OID(sqltype.Int4) || op.Result != ids.OID(sqltype.Float8) || op.Function != ext.Members[cubeRoutineMember(len(cubeRoutines)-2+i)].ObjectID || op.Commutator != 0 || op.Negator != 0 || op.Restriction != 0 || op.Join != 0 || op.Hash || op.Merge {
			return fmt.Errorf("stuntgres: invalid cube coordinate operator")
		}
	}
	return nil
}
