package extension

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func earthRoutineSignature(op sqltype.EarthOperation, earth sqltype.ID) catalog.ProcedureSignature {
	switch op {
	case sqltype.EarthRadiusOperation:
		return ""
	case sqltype.EarthLL:
		return "701,701"
	case sqltype.EarthLatitude, sqltype.EarthLongitude:
		return catalog.ProcedureSignature(strconv.FormatUint(uint64(earth), 10))
	default:
		return "701"
	}
}
func earthRoutineResult(op sqltype.EarthOperation, earth sqltype.ID) ids.OID {
	if op == sqltype.EarthLL {
		return ids.OID(earth)
	}
	return ids.OID(sqltype.Float8)
}
func (p *EarthdistanceProvider) stageRoutines(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, earth *sqltype.Descriptor) error {
	cube, err := earthCube(stage.Read())
	if err != nil {
		return err
	}
	cubeSchema := stage.Read().Get(cube.Namespace()).Name()
	for op := sqltype.EarthRadiusOperation; op <= sqltype.EarthLongitude; op++ {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		proc := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: op.Name(), Signature: earthRoutineSignature(op, earth.ID()), RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: earthRoutineResult(op, earth.ID()), Volatility: expr.Immutable, Strict: op != sqltype.EarthRadiusOperation, Display: catalog.RoutineDisplay{Language: catalog.SQLRoutineLanguage}, Implementation: p}
		proc.DeclarationBody, err = earthDeclarationBody(op, cubeSchema)
		if err != nil {
			return fmt.Errorf("stuntgres: invalid %s declaration body: %w", op.Name(), err)
		}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, proc.Name())}
		}
		ext.Members[int(op)+2] = proc.Address()
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return p.stagePoint(stage, ext, owner)
}
func (p *EarthdistanceProvider) ValidateProcedure(v catalog.Read, d *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, d, p)
}
