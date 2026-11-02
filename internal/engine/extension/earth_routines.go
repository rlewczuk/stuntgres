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

// EarthOperationSlot preserves the incumbent coordinate and point identities;
// query operations are appended after the point procedure/operator pair.
func EarthOperationSlot(op sqltype.EarthOperation) (int, bool) {
	switch op {
	case sqltype.EarthRadiusOperation, sqltype.EarthSecToGC, sqltype.EarthGCToSec, sqltype.EarthLL, sqltype.EarthLatitude, sqltype.EarthLongitude:
		return 2 + int(op), true
	case sqltype.EarthDistance:
		return EarthDistanceProcedureSlot, true
	case sqltype.EarthBox:
		return EarthBoxProcedureSlot, true
	}
	return 0, false
}

func EarthSlotOperation(slot int) (sqltype.EarthOperation, bool) {
	if slot >= 2 && slot < EarthPointProcedureSlot {
		return sqltype.EarthOperation(slot - 2), true
	}
	switch slot {
	case EarthDistanceProcedureSlot:
		return sqltype.EarthDistance, true
	case EarthBoxProcedureSlot:
		return sqltype.EarthBox, true
	}
	return 0, false
}

func earthRoutineSignature(op sqltype.EarthOperation, earth sqltype.ID) catalog.ProcedureSignature {
	switch op {
	case sqltype.EarthRadiusOperation:
		return ""
	case sqltype.EarthLL:
		return "701,701"
	case sqltype.EarthLatitude, sqltype.EarthLongitude:
		return catalog.ProcedureSignature(strconv.FormatUint(uint64(earth), 10))
	case sqltype.EarthDistance:
		id := strconv.FormatUint(uint64(earth), 10)
		return catalog.ProcedureSignature(id + "," + id)
	case sqltype.EarthBox:
		return catalog.ProcedureSignature(strconv.FormatUint(uint64(earth), 10) + ",701")
	default:
		return "701"
	}
}
func earthRoutineResult(op sqltype.EarthOperation, earth, cube sqltype.ID) ids.OID {
	if op == sqltype.EarthLL {
		return ids.OID(earth)
	}
	if op == sqltype.EarthBox {
		return ids.OID(cube)
	}
	return ids.OID(sqltype.Float8)
}
func (p *EarthdistanceProvider) stageRoutines(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, earth *sqltype.Descriptor) error {
	read := stage.CurrentRead()
	cube, err := earthCube(read)
	if err != nil {
		return err
	}
	cubeSchema := read.Get(cube.Namespace()).Name()
	for op := sqltype.EarthRadiusOperation; op <= sqltype.EarthBox; op++ {
		if op == sqltype.EarthDistance {
			if err := p.stagePoint(stage, ext, owner); err != nil {
				return err
			}
		}
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		proc := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: op.Name(), Signature: earthRoutineSignature(op, earth.ID()), RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: earthRoutineResult(op, earth.ID(), cube.SQLType.ID()), Volatility: expr.Immutable, Strict: op != sqltype.EarthRadiusOperation, Display: catalog.RoutineDisplay{Language: catalog.SQLRoutineLanguage}, Implementation: p}
		proc.DeclarationBody, err = earthDeclarationBody(op, cubeSchema)
		if err != nil {
			return fmt.Errorf("stuntgres: invalid %s declaration body: %w", op.Name(), err)
		}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, proc.Name())}
		}
		slot, _ := EarthOperationSlot(op)
		ext.Members[slot] = proc.Address()
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}
func (p *EarthdistanceProvider) ValidateProcedure(v catalog.Read, d *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, d, p)
}

// EarthQueryConversionHelper authenticates the same-installation conversion
// role, including survivors whose original extension is no longer installed.
func EarthQueryConversionHelper(v catalog.Read, query *catalog.Procedure) (*catalog.Procedure, error) {
	if query == nil || EarthdistanceProviderOf(query) != StockEarthdistance {
		return nil, InvalidProviderDescriptor()
	}
	installation, ok := catalog.OriginInstallation(query)
	if !ok || len(installation.Members) != EarthMemberCount {
		return nil, InvalidProviderDescriptor()
	}
	operation, ok := EarthSlotOperation(int(query.Origin.Declaration))
	if !ok || operation != sqltype.EarthDistance && operation != sqltype.EarthBox {
		return nil, InvalidProviderDescriptor()
	}
	conversion := sqltype.EarthSecToGC
	if operation == sqltype.EarthBox {
		conversion = sqltype.EarthGCToSec
	}
	slot, _ := EarthOperationSlot(conversion)
	helper, ok := v.Procedure(installation.Members[slot])
	if !ok || !catalog.SameInstallation(helper, installation) {
		return nil, InvalidProviderDescriptor()
	}
	symbol, err := ProcedureSymbol(v, helper)
	if err != nil || symbol != conversion.Name() {
		return nil, InvalidProviderDescriptor()
	}
	return helper, nil
}
