package exec

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// ValidateAggregateSupportMembership proves the actual owning declaration and
// selected support together. Neither an owner role nor an unrelated callable
// can be supplied as aggregate-support authority.
func ValidateAggregateSupportMembership(view catalog.Read, roles authorization.Read, aggregate, support *catalog.Procedure) (*catalog.Procedure, *catalog.Procedure, error) {
	invalid := func() (*catalog.Procedure, *catalog.Procedure, error) {
		return nil, nil, fmt.Errorf("stuntgres: invalid aggregate support membership")
	}
	if aggregate == nil || support == nil {
		return invalid()
	}
	current, err := ValidateRoutineDescriptor(view, roles, aggregate)
	if err != nil {
		return nil, nil, err
	}
	helper, err := ValidateRoutineDescriptor(view, roles, support)
	if err != nil {
		return nil, nil, err
	}
	if current.RoutineKind != catalog.AggregateRoutine || current.Aggregate == nil || helper.RoutineKind != catalog.FunctionRoutine {
		return invalid()
	}
	if current.Implementation != nil {
		if err = extension.ValidateProcedureOrigin(view, current, current.Implementation); err != nil {
			return nil, nil, err
		}
	}
	selected := false
	for _, oid := range current.Aggregate.Definition.Functions {
		if oid != 0 && helper.Address() == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}) {
			selected = true
			break
		}
	}
	if !selected {
		return invalid()
	}
	if current.Implementation == extension.StockIntagg {
		if err = extension.StockIntagg.ValidateProcedure(view, helper); err != nil {
			return nil, nil, err
		}
		matched := false
		for _, identity := range current.Aggregate.Helpers {
			if identity.ID == helper.Address().ObjectID && helper.Origin != nil && helper.Origin.Installation == current.Origin.Installation {
				matched = true
				break
			}
		}
		if !matched {
			return invalid()
		}
	}
	return current, helper, nil
}
