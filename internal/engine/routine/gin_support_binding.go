package routine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func bindGinSupport(view catalog.Read, class, captured sqltype.IndexClass, fact *catalog.OperatorClass, family *catalog.OperatorFamily) (sqltype.IndexClass, error) {
	if fact.Revision() == 0 || family.Revision() == 0 || view.Owner() == nil || view.Owner().Provider(ids.ProcedureClassID) == nil {
		return class, fmt.Errorf("stuntgres: unowned GIN provider capture")
	}
	input := ids.OID(2277)
	operators := []ids.OID{2750, 2751, 2752, 1070}
	right := input
	providerFamily := ids.OID(2745)
	switch {
	case fact.InputType == 2277 && fact.StorageType == 2283:
	case fact.InputType == 3614 && fact.StorageType == 25:
		input, right = 3614, 3615
		operators = []ids.OID{3636, 3660}
		providerFamily = 3659
	default:
		return class, fmt.Errorf("stuntgres: unsupported GIN token provider")
	}
	for slot := int16(1); slot <= 6; slot++ {
		expected, required := catalog.GinSupportRoutine(providerFamily, slot)
		member, exists := view.FamilySupport(fact.Family, input, input, slot)
		if !required {
			if exists {
				return class, fmt.Errorf("stuntgres: unexpected GIN support member")
			}
			continue
		}
		if !exists || member.Family != fact.Family || member.Left != input || member.Right != input || member.Number != slot || member.Function != expected || member.Revision() == 0 {
			return class, fmt.Errorf("stuntgres: invalid GIN support member")
		}
		proc, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: expected})
		if !ok || catalog.ValidateGinSupport(proc) != nil {
			return class, fmt.Errorf("stuntgres: unavailable GIN support capability")
		}
		if err := view.Owner().Provider(ids.ProcedureClassID).Validate(view, proc); err != nil {
			return class, err
		}
		support := sqltype.IndexSupport{Family: uint32(fact.Family), Member: uint32(member.Address().ObjectID), Routine: uint32(expected), Left: uint32(input), Right: uint32(input), Revision: uint64(member.Revision()), ClassRevision: uint64(fact.Revision()), FamilyRevision: uint64(family.Revision()), RoutineRevision: uint64(proc.Revision())}
		prior := captured.Support(int(slot))
		if prior != (sqltype.IndexSupport{}) && prior != support {
			return class, fmt.Errorf("stuntgres: stale GIN support capture")
		}
		class = class.WithSupport(int(slot), support)
	}
	for i, operator := range operators {
		member, ok := view.FamilyOperator(fact.Family, input, right, int16(i+1), 's')
		if !ok || member.Operator != operator || member.Method != fact.Method || member.SortFamily != 0 || member.Revision() == 0 {
			return class, fmt.Errorf("stuntgres: invalid GIN search strategy")
		}
	}
	return class, nil
}
