package extension

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type CubeProvider struct{ control catalog.ExtensionControl }

func (p *CubeProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *CubeProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.5"}}
}

var StockCube = &CubeProvider{control: catalog.ExtensionControl{Version: "1.5", Trusted: true, Superuser: true, Relocatable: true, Comment: new("data type for multidimensional cubes")}}

const CubeTypeContext = "SQL statement \"CREATE TYPE cube (\n\tINTERNALLENGTH = variable,\n\tINPUT = cube_in,\n\tOUTPUT = cube_out,\n\tALIGNMENT = double\n)\"\nextension script file \"cube--1.2.sql\", near line 26"
const CubeInputContext = "SQL statement \"CREATE FUNCTION cube_in(cstring)\nRETURNS cube\nAS '$libdir/cube'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"cube--1.2.sql\", near line 11"

func CubeSignature(id sqltype.ID) catalog.ProcedureSignature {
	return catalog.ProcedureSignature(strconv.FormatUint(uint64(id), 10))
}
func CubeComparisonSignature(id sqltype.ID) catalog.ProcedureSignature {
	s := string(CubeSignature(id))
	return catalog.ProcedureSignature(s + "," + s)
}

var CubeComparisonNames = [...]string{"cube_eq", "cube_ne", "cube_lt", "cube_le", "cube_gt", "cube_ge", "cube_cmp"}
var cubeOperatorNames = [...]string{"=", "<>", "<", "<=", ">", ">="}
var cubeCommutators = [...]int{0, 1, 4, 5, 2, 3}
var cubeNegators = [...]int{1, 0, 5, 4, 3, 2}
var cubeRestrictions = [...]ids.OID{101, 102, 103, 336, 104, 337}
var cubeJoins = [...]ids.OID{105, 106, 107, 386, 108, 398}
var cubeStrategies = [...]int{2, 3, 0, 5, 4}

func CubeComparisonName(name string) bool {
	for _, n := range CubeComparisonNames {
		if name == n {
			return true
		}
	}
	return false
}

// Zero denotes this installation's cube identity, never a builtin OID.
type cubeRoutineSpec struct {
	name   string
	args   [3]sqltype.ID
	nargs  int
	result sqltype.ID
}

var cubeRoutines = [...]cubeRoutineSpec{
	{"cube_in", [3]sqltype.ID{2275}, 1, 0},
	{"cube_out", [3]sqltype.ID{0}, 1, 2275},
	{CubeComparisonNames[0], [3]sqltype.ID{0, 0}, 2, sqltype.Bool},
	{CubeComparisonNames[1], [3]sqltype.ID{0, 0}, 2, sqltype.Bool},
	{CubeComparisonNames[2], [3]sqltype.ID{0, 0}, 2, sqltype.Bool},
	{CubeComparisonNames[3], [3]sqltype.ID{0, 0}, 2, sqltype.Bool},
	{CubeComparisonNames[4], [3]sqltype.ID{0, 0}, 2, sqltype.Bool},
	{CubeComparisonNames[5], [3]sqltype.ID{0, 0}, 2, sqltype.Bool},
	{CubeComparisonNames[6], [3]sqltype.ID{0, 0}, 2, sqltype.Int4},
	{"cube", [3]sqltype.ID{sqltype.Float8}, 1, 0},
	{"cube", [3]sqltype.ID{sqltype.Float8, sqltype.Float8}, 2, 0},
	{"cube", [3]sqltype.ID{1022}, 1, 0},
	{"cube", [3]sqltype.ID{1022, 1022}, 2, 0},
	{"cube", [3]sqltype.ID{0, sqltype.Float8}, 2, 0},
	{"cube", [3]sqltype.ID{0, sqltype.Float8, sqltype.Float8}, 3, 0},
	{"cube_dim", [3]sqltype.ID{0}, 1, sqltype.Int4},
	{"cube_is_point", [3]sqltype.ID{0}, 1, sqltype.Bool},
	{"cube_ll_coord", [3]sqltype.ID{0, sqltype.Int4}, 2, sqltype.Float8},
	{"cube_ur_coord", [3]sqltype.ID{0, sqltype.Int4}, 2, sqltype.Float8},
	{"cube_coord", [3]sqltype.ID{0, sqltype.Int4}, 2, sqltype.Float8},
	{"cube_coord_llur", [3]sqltype.ID{0, sqltype.Int4}, 2, sqltype.Float8},
}

// The selected comparison graph keeps its stable prefix. Other installed
// routines follow it; each existing registry retains its feature authority.
const (
	CubeComparisonMemberCount   = 18
	cubeCoordinateOperatorStart = CubeComparisonMemberCount + len(cubeRoutines) - 2 - len(CubeComparisonNames)
	cubeSpatialProcedureStart   = cubeCoordinateOperatorStart + len(cubeCoordinateOperators)
	cubeSpatialOperatorStart    = cubeSpatialProcedureStart + len(cubeSpatialMembers)
	CubeMemberCount             = cubeSpatialOperatorStart + len(cubeSpatialMembers)
)

func cubeRoutineMember(index int) int {
	if index < 2+len(CubeComparisonNames) {
		return index + 1
	}
	return CubeComparisonMemberCount + index - 2 - len(CubeComparisonNames)
}

func (s cubeRoutineSpec) signature(cube sqltype.ID) catalog.ProcedureSignature {
	var parts [3]string
	for i := range s.nargs {
		id := s.args[i]
		if id == 0 {
			id = cube
		}
		parts[i] = strconv.FormatUint(uint64(id), 10)
	}
	return catalog.ProcedureSignature(strings.Join(parts[:s.nargs], ","))
}

func (s cubeRoutineSpec) ownerSignature() string {
	var parts [3]string
	for i := range s.nargs {
		if s.args[i] == 0 {
			parts[i] = "cube"
		} else {
			parts[i] = strconv.FormatUint(uint64(s.args[i]), 10)
		}
	}
	return strings.Join(parts[:s.nargs], ",")
}

func (s cubeRoutineSpec) resultOID(cube sqltype.ID) ids.OID {
	if s.result == 0 {
		return ids.OID(cube)
	}
	return ids.OID(s.result)
}

func (p *CubeProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid cube manifest") }
	if p != StockCube || ext.Name() != "cube" || ext.Version != p.control.Version || len(ext.Members) != CubeMemberCount {
		return invalid()
	}
	if ext.Members[0].ClassID != ids.TypeClassID || ext.Members[1].ClassID != ids.ProcedureClassID || ext.Members[2].ClassID != ids.ProcedureClassID {
		return invalid()
	}
	base, ok := v.Type(ext.Members[0])
	if !ok || base.SQLType == nil || base.Address() != ext.Members[0] || !base.SQLType.IsCube() || base.Extension != ext.Address() || base.ObjectRevision == 0 || base.Array == 0 {
		return invalid()
	}
	array, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
	if !ok || array.SQLType == nil || !array.SQLType.IsArray() || array.SQLType.Concrete() || array.SQLType.Element() != base.SQLType || array.Namespace() != base.Namespace() || array.Owner != base.Owner || array.Extension != (ids.ObjectAddress{}) {
		return invalid()
	}
	if err := validateRegisteredType(v, array); err != nil {
		return err
	}
	for i, spec := range cubeRoutines {
		address := ext.Members[cubeRoutineMember(i)]
		proc, ok := v.Procedure(address)
		if !ok || address.ClassID != ids.ProcedureClassID || proc.Address() != address || CubeProviderOf(proc) != p || proc.Extension != ext.Address() || proc.Namespace() != ext.TargetNamespace || proc.Revision() == 0 {
			return invalid()
		}
		name, signature, result := spec.name, spec.signature(base.SQLType.ID()), spec.resultOID(base.SQLType.ID())
		if proc.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(proc)) != address || proc.RoutineKind != catalog.FunctionRoutine || proc.Aggregate != nil || proc.Foreign != nil || proc.Builtin != nil || proc.Name() != name || proc.Signature != signature || proc.Result != result || !proc.Strict || proc.Volatility != expr.Immutable || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) {
			return invalid()
		}
	}
	for i, address := range ext.Members[10:16] {
		o, ok := v.Operator(address)
		if ok && o.Address() != address {
			return invalid()
		}
		if !ok || o.Namespace() != ext.TargetNamespace || o.Extension != ext.Address() || o.Revision() == 0 || o.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(o)) != address || o.Name() != cubeOperatorNames[i] || o.Left != ids.OID(base.SQLType.ID()) || o.Right != o.Left || o.Result != 16 || o.Function != ext.Members[i+3].ObjectID || o.Commutator != ext.Members[10+cubeCommutators[i]].ObjectID || o.Negator != ext.Members[10+cubeNegators[i]].ObjectID || o.Hash || o.Merge != (i == 0) || o.Restriction != cubeRestrictions[i] || o.Join != cubeJoins[i] {
			return invalid()
		}
	}
	family, ok := v.OperatorFamily(ext.Members[16])
	if ok && family.Address() != ext.Members[16] {
		return invalid()
	}
	if !ok || family.Name() != "cube_ops" || family.Method != 403 || family.Extension != ext.Address() || family.Namespace() != ext.TargetNamespace || family.Owner == (authorization.PrincipalRef{}) || family.Revision() == 0 || v.NameAddress(catalog.DescriptorNameKey(family)) != family.Address() {
		return invalid()
	}
	class, ok := v.OperatorClass(ext.Members[17].ObjectID)
	if !ok || class.Address() != ext.Members[17] || class.Name() != "cube_ops" || class.Method != 403 || class.Family != family.Address().ObjectID || class.InputType != ids.OID(base.SQLType.ID()) || class.StorageType != 0 || !class.Default || class.Extension != ext.Address() || class.Namespace() != ext.TargetNamespace || class.Owner == (authorization.PrincipalRef{}) || class.Revision() == 0 || v.NameAddress(catalog.DescriptorNameKey(class)) != class.Address() {
		return invalid()
	}
	for i, operator := range cubeStrategies {
		m, ok := v.FamilyOperator(class.Family, class.InputType, class.InputType, int16(i+1), 's')
		if !ok || m.Family != class.Family || m.Left != class.InputType || m.Right != class.InputType || m.Number != int16(i+1) || m.Purpose != 's' || m.Operator != ext.Members[10+operator].ObjectID || m.Method != 403 || m.SortFamily != 0 || m.OwningClass != class.Address().ObjectID || m.Revision() == 0 {
			return invalid()
		}
	}
	support, ok := v.FamilySupport(class.Family, class.InputType, class.InputType, 1)
	if !ok || support.Family != class.Family || support.Left != class.InputType || support.Right != class.InputType || support.Number != 1 || support.Function != ext.Members[9].ObjectID || support.OwningClass != class.Address().ObjectID || support.Revision() == 0 {
		return invalid()
	}
	if err := p.validateCoordinates(v, ext, base); err != nil {
		return err
	}
	return p.validateSpatial(v, ext, base)
}

func (p *CubeProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing cube routine installation")
	}
	return p.ValidateExtension(v, ext)
}
