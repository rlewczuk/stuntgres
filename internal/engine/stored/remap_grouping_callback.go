package stored

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

func (m *ViewQueryRemapper) remapGroupingKernel(old sqltype.Grouping, usage sqltype.Usage) (sqltype.Grouping, error) {
	next, err := sqltype.BindGrouping(usage)
	if err != nil {
		return sqltype.Grouping{}, err
	}
	if m.referencesOnly {
		next = old
	}
	return m.remapSelectedGroupingKernel(next, old)
}

func (m *ViewQueryRemapper) remapSelectedGroupingKernel(next, old sqltype.Grouping) (sqltype.Grouping, error) {
	var err error
	return next.RemapCallbacks(old, func(reference sqltype.GroupingCallbackReference) (sqltype.GroupingCallbackReference, error) {
		original, ok := reference.(catalog.GroupingRoutineCallback)
		if !ok {
			return nil, fmt.Errorf("stuntgres: invalid remapped grouping callback")
		}
		callback := original
		callback.Type = m.Type(original.Type)
		if m.Ref != nil {
			callback.Reference = m.Ref(original.Reference)
		}
		callback.Procedure, err = m.procedure(original.Procedure)
		if err != nil {
			return nil, err
		}
		if m.referencesOnly {
			if m.Ref != nil {
				callback.Class = m.Ref(original.Class)
			}
			return callback, nil
		}
		if callback.Hash {
			if m.Ref != nil {
				callback.Class = m.Ref(original.Class)
			}
			class, ok := m.Catalog.IndexClass(callback.Class.Address.ObjectID)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing remapped grouping hash class")
			}
			class, err = routine.BindClassSupport(m.Catalog, class)
			if err != nil {
				return nil, err
			}
			callback.Support = class.Support(2)
			if callback.Support.Routine != uint32(callback.Reference.Address.ObjectID) {
				return nil, fmt.Errorf("stuntgres: remapped grouping hash identity changed")
			}
		}
		if err := callback.Validate(m.Catalog); err != nil {
			return nil, err
		}
		return callback, nil
	})
}

func (m *ViewQueryRemapper) remapRecipeGrouping(old plan.Grouping, usage sqltype.Usage) (plan.Grouping, error) {
	next := old
	if m.Ref != nil {
		next.Routine = m.Ref(old.Routine)
	}
	next.Source.Usage = usage
	var err error
	next.Procedure, err = m.procedure(old.Procedure)
	if err != nil {
		return plan.Grouping{}, err
	}
	next.Grouping, err = m.remapGroupingKernel(old.Grouping, usage)
	if err != nil {
		return plan.Grouping{}, err
	}
	if old.Convert {
		target, _ := old.Converted.Selection()
		target.Type = m.Type(target.Type)
		next.Conversion, err = sqltype.PlanCoercion(usage, target, sqltype.Implicit)
		if err != nil {
			return plan.Grouping{}, err
		}
		next.Converted, err = m.remapGroupingKernel(old.Converted, target)
	}
	return next, err
}
