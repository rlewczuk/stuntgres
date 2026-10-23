package extension

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var cubeSpatialMembers = [...]struct {
	operator          string
	operation         sqltype.CubeSpatialOperation
	commutator        int
	restriction, join ids.OID
}{
	{"&&", sqltype.CubeOverlap, 0, 139, 140},
	{"@>", sqltype.CubeContains, 2, 1302, 1303},
	{"<@", sqltype.CubeContained, 1, 1302, 1303},
	{"<->", sqltype.CubeDistance, 3, 0, 0},
	{"<#>", sqltype.CubeTaxicab, 4, 0, 0},
	{"<=>", sqltype.CubeChebyshev, 5, 0, 0},
}

func CubeBinarySignature(id sqltype.ID) catalog.ProcedureSignature {
	one := CubeSignature(id)
	return one + "," + one
}

// Check the canonical binary signature without formatting allocator identities
// on each evaluation.
func cubeBinaryInputs(signature catalog.ProcedureSignature, id sqltype.ID) bool {
	first, second, binary := strings.Cut(string(signature), ",")
	if !binary || first != second || first == "" || first[0] < '1' || first[0] > '9' {
		return false
	}
	actual, err := strconv.ParseUint(first, 10, 32)
	return err == nil && actual == uint64(id)
}

func (p *CubeProvider) stageSpatial(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, typ *sqltype.Descriptor) error {
	signature := CubeBinarySignature(typ.ID())
	for i, member := range cubeSpatialMembers {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		proc := &catalog.Procedure{ObjectAddress: ext.Members[cubeSpatialProcedureStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.operation.Routine(), Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: ids.OID(member.operation.ResultID()), Volatility: expr.Immutable, Strict: true, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, proc.Name())}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
		revision, err = stage.Revision()
		if err != nil {
			return err
		}
		op := &catalog.Operator{ObjectAddress: ext.Members[cubeSpatialOperatorStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.operator, Left: ids.OID(typ.ID()), Right: ids.OID(typ.ID()), Owner: owner, Result: proc.Result, Function: proc.Address().ObjectID, Commutator: ext.Members[cubeSpatialOperatorStart+member.commutator].ObjectID, Restriction: member.restriction, Join: member.join}
		if stage.LookupName(catalog.DescriptorNameKey(op)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: fmt.Sprintf(`operator %s already exists`, op.Name())}
		}
		if err := stage.Create(op); err != nil {
			return err
		}
	}
	return nil
}

func (p *CubeProvider) validateSpatial(v catalog.Read, ext *catalog.Extension, base *catalog.Type) error {
	for i, member := range cubeSpatialMembers {
		proc, ok := v.Procedure(ext.Members[cubeSpatialProcedureStart+i])
		if !ok || proc.Address().ClassID != ids.ProcedureClassID || proc.Address() != ext.Members[cubeSpatialProcedureStart+i] || proc.Namespace() != ext.TargetNamespace || proc.Extension != ext.Address() || proc.Revision() == 0 || proc.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(proc)) != proc.Address() || CubeProviderOf(proc) != p || proc.Name() != member.operation.Routine() || !cubeBinaryInputs(proc.Signature, base.SQLType.ID()) || proc.Result != ids.OID(member.operation.ResultID()) || proc.RoutineKind != catalog.FunctionRoutine || !proc.Strict || proc.Volatility != expr.Immutable || proc.SQL != nil || proc.Builtin != nil || proc.Aggregate != nil || proc.Foreign != nil || proc.Dictionary != (ids.ObjectAddress{}) || proc.Conversion.Name() != "" {
			return fmt.Errorf("stuntgres: invalid cube spatial procedure")
		}
		op, ok := v.Operator(ext.Members[cubeSpatialOperatorStart+i])
		if !ok || op.Address().ClassID != ids.OperatorClassID || op.Address() != ext.Members[cubeSpatialOperatorStart+i] || op.Namespace() != ext.TargetNamespace || op.Extension != ext.Address() || op.Revision() == 0 || op.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(op)) != op.Address() || op.Name() != member.operator || op.Left != ids.OID(base.SQLType.ID()) || op.Right != op.Left || op.Result != proc.Result || op.Function != proc.Address().ObjectID || op.Commutator != ext.Members[cubeSpatialOperatorStart+member.commutator].ObjectID || op.Negator != 0 || op.Restriction != member.restriction || op.Join != member.join || op.Hash || op.Merge {
			return fmt.Errorf("stuntgres: invalid cube spatial operator")
		}
	}
	return nil
}

func cubeSpatialOwnerName(name string, operator bool) bool {
	for _, member := range cubeSpatialMembers {
		if operator && name == member.operator || !operator && name == member.operation.Routine() {
			return true
		}
	}
	return false
}
