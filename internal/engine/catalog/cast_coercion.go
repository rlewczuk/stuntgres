package catalog

import (
	"fmt"

	"stuntgres/internal/sqltype"
)

// SelectCoercion is effect-free. A declared pair that disallows the context
// blocks generic fallback. There is deliberately no transitive graph search.
func SelectCoercion(view Read, source, target sqltype.Usage, mode sqltype.CoercionContext) (*Cast, error) {
	if source.Type == nil || target.Type == nil {
		return nil, fmt.Errorf("invalid coercion usage")
	}
	if source.Type.IsDomain() || target.Type.IsDomain() || source == target {
		return nil, nil
	}
	if d, ok := view.Cast(source.Type.ID(), target.Type.ID()); ok {
		if d.Context == "i" || d.Context == "a" && mode != sqltype.Implicit || mode == sqltype.Explicit {
			return d, nil
		}
		code := "42804"
		if mode == sqltype.Explicit {
			code = "42846"
		}
		return nil, sqlError(code, fmt.Sprintf("cannot cast type %s to %s", source.Type.DiagnosticName(), target.Type.DiagnosticName()))
	}
	return nil, nil
}

func CanCoerce(view Read, source, target sqltype.Usage, mode sqltype.CoercionContext) error {
	if source.Type == nil || target.Type == nil {
		return fmt.Errorf("invalid coercion usage")
	}
	if source.Type.IsDomain() || target.Type.IsDomain() {
		return CanCoerce(view, source.BaseUsage(), target.BaseUsage(), mode)
	}
	if source.Type.IsArray() && target.Type.IsArray() {
		if _, exists := view.Cast(source.Type.ID(), target.Type.ID()); !exists {
			from, to := source.Type.Element().Usage(), target.Type.Element().Usage()
			from.Typmod, to.Typmod = source.Typmod, target.Typmod
			from.Collation, to.Collation = source.Collation, target.Collation
			return CanCoerce(view, from, to, mode)
		}
	}
	d, err := SelectCoercion(view, source, target, mode)
	if err != nil || d != nil {
		return err
	}
	_, err = sqltype.PlanCoercion(source, target, mode)
	return err
}
