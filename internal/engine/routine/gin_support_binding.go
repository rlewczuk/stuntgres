package routine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func bindGinSupport(view catalog.Read, class, captured sqltype.IndexClass, fact *catalog.OperatorClass, family *catalog.OperatorFamily) (sqltype.IndexClass, error) {
	if fact.Revision() == 0 || family.Revision() == 0 || view.Owner() == nil || view.Owner().Provider(ids.ProcedureClassID) == nil {
		return class, fmt.Errorf("stuntgres: unowned GIN provider capture")
	}
	if profile, ok := LookupGinProfile(fact.Family); ok {
		return bindGinProfile(view, class, captured, fact, family, profile)
	}
	input := ids.OID(2277)
	operators := []ids.OID{2750, 2751, 2752, 1070}
	left, right := input, input
	providerFamily := ids.OID(2745)
	var scalarSupport [5]ids.OID
	scalar := false
	switch {
	case fact.InputType == 2277 && fact.StorageType == 2283:
	case fact.InputType == 3614 && fact.StorageType == 25:
		input, left, right = 3614, 3614, 3615
		operators = []ids.OID{3636, 3660}
		providerFamily = 3659
	default:
		bindings, err := extension.BtreeGinClassBindings(view, fact)
		if err != nil {
			return class, err
		}
		scalar = true
		scalarSupport = bindings.Support
		input = fact.InputType
		left, right = bindings.SearchType, bindings.SearchType
		operators = bindings.Search[:]
	}
	for slot := int16(1); slot <= 6; slot++ {
		expected, required := catalog.GinSupportRoutine(providerFamily, slot)
		if scalar {
			required = slot <= 5
			if required {
				expected = scalarSupport[slot-1]
			}
		}
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
		if !ok || !scalar && catalog.ValidateGinSupport(proc) != nil {
			return class, fmt.Errorf("stuntgres: unavailable GIN support capability")
		}
		if err := view.Owner().Provider(ids.ProcedureClassID).Validate(view, proc); err != nil {
			return class, err
		}
		support := sqltype.IndexSupport{Class: uint32(fact.Address().ObjectID), Family: uint32(fact.Family), Member: uint32(member.Address().ObjectID), Routine: uint32(expected), Left: uint32(input), Right: uint32(input), Revision: uint64(member.Revision()), ClassRevision: uint64(fact.Revision()), FamilyRevision: uint64(family.Revision()), RoutineRevision: uint64(proc.Revision())}
		if scalar {
			support.Authority = fact.Origin.Installation.IndexAuthority()
		}
		prior := captured.Support(int(slot))
		if prior != (sqltype.IndexSupport{}) && prior != support {
			return class, fmt.Errorf("stuntgres: stale GIN support capture")
		}
		class = class.WithSupport(int(slot), support)
	}
	for i, operator := range operators {
		member, ok := view.FamilyOperator(fact.Family, left, right, int16(i+1), 's')
		if !ok || member.Operator != operator || member.Method != fact.Method || member.SortFamily != 0 || member.Revision() == 0 {
			return class, fmt.Errorf("stuntgres: invalid GIN search strategy")
		}
		operatorFact, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: operator})
		if !ok || operatorFact.Left != left || operatorFact.Right != right || operatorFact.Result != 16 {
			return class, fmt.Errorf("stuntgres: invalid GIN operator authority")
		}
		provider := view.Owner().Provider(ids.OperatorClassID)
		if provider == nil {
			return class, fmt.Errorf("stuntgres: unavailable GIN operator provider")
		}
		if err := provider.Validate(view, operatorFact); err != nil {
			return class, err
		}
		procedure, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: operatorFact.Function})
		if !ok {
			return class, fmt.Errorf("stuntgres: unavailable GIN operator routine")
		}
		if err := view.Owner().Provider(ids.ProcedureClassID).Validate(view, procedure); err != nil {
			return class, err
		}
		search := sqltype.IndexSearch{Member: uint32(member.Address().ObjectID), Operator: uint32(operator), Routine: uint32(procedure.Address().ObjectID), MemberRevision: uint64(member.Revision()), OperatorRevision: uint64(operatorFact.Revision()), RoutineRevision: uint64(procedure.Revision())}
		if prior := captured.Search(i + 1); prior != (sqltype.IndexSearch{}) && prior != search {
			return class, fmt.Errorf("stuntgres: stale GIN search capture")
		}
		class = class.WithSearch(i+1, search)
	}
	return class, nil
}

// bindGinProfile binds one native family through its declared support slots and
// strategy ordinals. Declared ordinals are list positions, not strategy
// numbers: jsonb_ops declares 7/9/10/11/15/16.
func bindGinProfile(view catalog.Read, class, captured sqltype.IndexClass, fact *catalog.OperatorClass, family *catalog.OperatorFamily, profile GinProfile) (sqltype.IndexClass, error) {
	input := profile.Input
	for slot := int16(1); slot <= 6; slot++ {
		declared, required := profile.SupportFor(slot)
		member, exists := view.FamilySupport(fact.Family, input, input, slot)
		if !required {
			if exists {
				return class, fmt.Errorf("stuntgres: unexpected GIN support member")
			}
			continue
		}
		if !exists || member.Family != fact.Family || member.Left != input || member.Right != input || member.Number != slot || member.Function != declared.Routine || member.Revision() == 0 {
			return class, fmt.Errorf("stuntgres: invalid GIN support member")
		}
		var support sqltype.IndexSupport
		if declared.Intrinsic {
			if expected, ok := catalog.GinSupportRoutine(fact.Family, slot); !ok || expected != declared.Routine {
				return class, fmt.Errorf("stuntgres: invalid GIN support identity")
			}
			proc, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declared.Routine})
			if !ok || catalog.ValidateGinSupport(proc) != nil {
				return class, fmt.Errorf("stuntgres: unavailable GIN support capability")
			}
			if err := view.Owner().Provider(ids.ProcedureClassID).Validate(view, proc); err != nil {
				return class, err
			}
			support = sqltype.IndexSupport{Class: uint32(fact.Address().ObjectID), Family: uint32(fact.Family), Member: uint32(member.Address().ObjectID), Routine: uint32(declared.Routine), Left: uint32(input), Right: uint32(input), Revision: uint64(member.Revision()), ClassRevision: uint64(fact.Revision()), FamilyRevision: uint64(family.Revision()), RoutineRevision: uint64(proc.Revision())}
		} else {
			var err error
			support, err = selectNativeIndexSupport(view, uint32(fact.Family), uint32(input), uint32(input), slot)
			if err != nil {
				return class, err
			}
			proc, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declared.Routine})
			if !ok || proc.GinSupport != 0 {
				return class, fmt.Errorf("stuntgres: unavailable GIN support capability")
			}
			support.Class = uint32(fact.Address().ObjectID)
			support.ClassRevision = uint64(fact.Revision())
		}
		prior := captured.Support(int(slot))
		if prior != (sqltype.IndexSupport{}) && prior != support {
			return class, fmt.Errorf("stuntgres: stale GIN support capture")
		}
		class = class.WithSupport(int(slot), support)
	}
	provider := view.Owner().Provider(ids.OperatorClassID)
	if provider == nil {
		return class, fmt.Errorf("stuntgres: unavailable GIN operator provider")
	}
	for i, strategy := range profile.Strategies {
		ordinal := i + 1
		member, ok := view.FamilyOperator(fact.Family, strategy.Left, strategy.Right, int16(strategy.Number), 's')
		if !ok || member.Operator != strategy.Operator || member.Method != fact.Method || member.SortFamily != 0 || member.Revision() == 0 {
			return class, fmt.Errorf("stuntgres: invalid GIN search strategy")
		}
		operatorFact, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: strategy.Operator})
		if !ok || operatorFact.Left != strategy.Left || operatorFact.Right != strategy.Right || operatorFact.Result != 16 {
			return class, fmt.Errorf("stuntgres: invalid GIN operator authority")
		}
		if err := provider.Validate(view, operatorFact); err != nil {
			return class, err
		}
		search := sqltype.IndexSearch{Member: uint32(member.Address().ObjectID), Operator: uint32(strategy.Operator), MemberRevision: uint64(member.Revision()), OperatorRevision: uint64(operatorFact.Revision())}
		if procedure, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: operatorFact.Function}); ok {
			if err := view.Owner().Provider(ids.ProcedureClassID).Validate(view, procedure); err != nil {
				return class, err
			}
			search.Routine = uint32(operatorFact.Function)
			search.RoutineRevision = uint64(procedure.Revision())
		} else {
			if err := GinMetadataOnlyOperator(view, operatorFact); err != nil {
				return class, err
			}
			// Lookup-only released identities (4047/4048/4049) grant no
			// callable authority and therefore carry no routine revision.
			search.Routine = uint32(operatorFact.Function)
		}
		if prior := captured.Search(ordinal); prior != (sqltype.IndexSearch{}) && prior != search {
			return class, fmt.Errorf("stuntgres: stale GIN search capture")
		}
		class = class.WithSearch(ordinal, search)
	}
	return class, nil
}

// GinMetadataOnlyOperator accepts a seeded released operator whose backing
// identity is deliberately not installed as a pg_proc descriptor. The released
// signature and result must still match the operator's formal declaration.
func GinMetadataOnlyOperator(view catalog.Read, operator *catalog.Operator) error {
	if !OperatorSeeded(operator) {
		return fmt.Errorf("stuntgres: unavailable GIN operator routine")
	}
	identity, ok := catalog.BuiltinRoutineIdentityByOID(operator.Function)
	if !ok || identity.Namespace != ids.PGCatalogNamespaceOID {
		return fmt.Errorf("stuntgres: unavailable GIN operator routine")
	}
	signature := strconv.FormatUint(uint64(operator.Left), 10) + "," + strconv.FormatUint(uint64(operator.Right), 10)
	if string(identity.Signature) != signature {
		return fmt.Errorf("stuntgres: unavailable GIN operator routine")
	}
	result, ok := catalog.BuiltinRoutineResultByOID(operator.Function)
	if !ok || result != operator.Result {
		return fmt.Errorf("stuntgres: unavailable GIN operator routine")
	}
	if _, installed := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: operator.Function}); installed {
		return fmt.Errorf("stuntgres: unavailable GIN operator routine")
	}
	return nil
}