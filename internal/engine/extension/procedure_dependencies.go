package extension

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// VisitProcedureDependencies implements catalog's released-body dependency
// contract. Repeated calls in a parsed body contribute one direct normal edge.
func (provider *EarthdistanceProvider) VisitProcedureDependencies(v catalog.Read, p *catalog.Procedure, yield func(ids.ObjectAddress) bool) bool {
	if provider != StockEarthdistance || p == nil || p.Implementation != provider || !p.Origin.Valid(p.Address()) || p.Origin.Installation.Version != "1.2" {
		return true
	}
	recipe := p.Origin.Installation
	op := sqltype.EarthOperation(p.Origin.Declaration - 2)
	switch op {
	case sqltype.EarthSecToGC, sqltype.EarthGCToSec, sqltype.EarthLL, sqltype.EarthLatitude:
		if !yield(recipe.Members[2]) {
			return false
		}
	}
	if op != sqltype.EarthLL && op != sqltype.EarthLatitude && op != sqltype.EarthLongitude {
		return true
	}
	// The sealed domain binds the prerequisite nominal type even after either
	// original extension has been removed. Captured body spelling is not lookup
	// authority, and another same-named cube installation is not interchangeable.
	domain, ok := recipe.Declarations[0].(*catalog.Type)
	if !ok || domain.SQLType == nil || !domain.SQLType.IsDomain() {
		return true
	}
	base := domain.SQLType.DomainBase().Type
	cube, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.ID())})
	if !ok || cube.SQLType == nil || !cube.SQLType.IsCube() || !cube.Origin.Valid(cube.Address()) || cube.Origin.Installation.Implementation != StockCube || cube.Origin.Declaration != 0 {
		return true
	}
	members := cube.Origin.Installation.Members
	if op == sqltype.EarthLL {
		return yield(members[cubeRoutineMember(cubeFloat8RoutineIndex)]) && yield(members[cubeRoutineMember(cubeAppendFloat8RoutineIndex)])
	}
	return yield(members[cubeRoutineMember(cubeLowerCoordinateRoutineIndex)])
}
