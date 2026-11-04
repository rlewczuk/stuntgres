package exec

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func groupingCallbackSite(callback catalog.GroupingRoutineCallback) DirectRoutineInitializationSite {
	purpose, ordinal := DirectGroupingEqual, uint16(0)
	if callback.Hash {
		purpose, ordinal = DirectHash, 2
	}
	return DirectRoutineInitializationSite{Routine: callback.Procedure, Reference: callback.Reference, Purpose: purpose, Source: callback.Class, Ordinal: ordinal, Occurrence: callback.Occurrence, ExpressionSource: callback.ExpressionSource}
}
func InitializeGroupingCallbacks(c *Context, g plan.Grouping) error {
	group := g.Grouping
	if g.Convert {
		group = g.Converted
	}
	if err := group.VisitIndexSupports(func(support sqltype.IndexSupport, slot uint16) error { return InitializeIndexSupport(c, support, slot) }); err != nil {
		return err
	}
	return group.VisitCallbacks(func(reference sqltype.GroupingCallbackReference) error {
		callback, ok := reference.(catalog.GroupingRoutineCallback)
		if !ok {
			return fmt.Errorf("stuntgres: invalid selected grouping callback")
		}
		if err := callback.Validate(c.Catalog); err != nil {
			return err
		}
		return BeginDirectRoutineInitialization(c, groupingCallbackSite(callback))
	})
}
func (c *Context) EnterGroupingCallback(reference sqltype.GroupingCallbackReference) (sqltype.RoutineBodyToken, error) {
	callback, ok := reference.(catalog.GroupingRoutineCallback)
	if !ok || c == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: missing grouping invocation authority")
	}
	if err := callback.Validate(c.Catalog); err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	if owner := c.Catalog.Owner(); owner == nil || owner.Sepgsql == nil {
		return sqltype.RoutineBodyToken{}, nil
	}
	if c.ClosedEvaluation != nil {
		return c.EnterInitializedDirectRoutine(groupingCallbackSite(callback))
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: missing live grouping callback owner")
	}
	if owner := c.Catalog.Owner(); owner != nil && owner.Sepgsql != nil && callback.IndexEquality == nil {
		if err := host.ValidateDirectRoutineInitialization(c.RoutineInitialization, groupingCallbackSite(callback)); err != nil {
			return sqltype.RoutineBodyToken{}, err
		}
	}
	return host.EnterGroupingCallback(callback)
}
