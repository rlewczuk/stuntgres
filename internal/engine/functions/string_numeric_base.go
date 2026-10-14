package functions

import "stuntgres/internal/sqltype"

// Keep the original lazy target demand: an exact INT4 match never looks up INT8.
func StringNumericBaseCandidates[E any](visible bool, args []Argument[E]) (ids [2]sqltype.ID, count int) {
	if !visible || len(args) != 1 {
		return ids, 0
	}
	ids = [2]sqltype.ID{sqltype.Int4, sqltype.Int8}
	if d := args[0].Usage.Type; d != nil && d.BaseType().ID() == sqltype.Int4 {
		return ids, 1
	}
	return ids, 2
}
func SelectStringNumericBase[E any](visible bool, args []Argument[E], targets []sqltype.Usage) (target sqltype.Usage, resolution Resolution) {
	matches := 0
	if visible && len(args) == 1 {
		argument := args[0]
		for _, candidate := range targets {
			if d := argument.Usage.Type; d != nil {
				if d.BaseType().ID() == candidate.Type.ID() {
					target, matches = candidate, 1
					break
				}
				if _, err := sqltype.PlanCoercion(argument.Usage, candidate, sqltype.Implicit); err != nil {
					continue
				}
			}
			target = candidate
			matches++
		}
	}
	if matches == 1 {
		return target, Resolved
	}
	if matches > 1 {
		return target, Ambiguous
	}
	return target, Missing
}
func CoerceStringNumericBase[E any, B ArgumentCoercer[E]](b B, argument Argument[E], target sqltype.Usage) (E, error) {
	return b.CoerceArgument(argument, CoercionRequest{Intent: ExactFormalArgument, Target: target, PreserveProperties: true})
}
