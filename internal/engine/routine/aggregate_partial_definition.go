package routine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// This capability is declaration-only: its implementation operates on the
// private string aggregate state, never on public SQL internal values.
func aggregateInternalSupport(d *catalog.Procedure, slot int) bool {
	if d == nil || d.Builtin == nil || d.Namespace().ObjectID != ids.PGCatalogNamespaceOID {
		return false
	}
	id, name, signature, result, strict := ids.OID(6299), "string_agg_combine", catalog.ProcedureSignature("2281,2281"), ids.OID(2281), false
	switch slot {
	case 0, 2:
	case 3:
		id, name, signature, result, strict = 6300, "string_agg_serialize", "2281", 17, true
	case 4:
		id, name, signature, result, strict = 6301, "string_agg_deserialize", "17,2281", 2281, true
	default:
		return false
	}
	identity := catalog.RoutineIdentity{ID: id, Namespace: ids.PGCatalogNamespaceOID, Name: name, Signature: signature}
	return d.Address().ObjectID == id && d.Builtin.Identity == identity && d.Name() == name && d.Signature == signature && d.Result == result && d.Strict == strict
}

func validateAggregatePartialDefinition(v catalog.Read, d *catalog.Procedure, inputs []sqltype.Usage) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid aggregate partial definition") }
	a := &d.Aggregate.Definition
	if p := d.Aggregate.Parallel; p != 0 && p != 's' && p != 'r' && p != 'u' {
		return invalid()
	}
	if (a.Functions[3] == 0) != (a.Functions[4] == 0) || a.Functions[3] != 0 && a.TransitionType != 2281 {
		return invalid()
	}
	state, ok := v.LookupType(sqltype.ID(a.TransitionType))
	if !ok {
		return invalid()
	}
	if d.Result == 2281 {
		safe := false
		for _, u := range inputs {
			safe = safe || u.Type.ID() == 2281
		}
		if !safe {
			return invalid()
		}
	}
	for slot := 2; slot <= 4; slot++ {
		id := a.Functions[slot]
		if id == 0 {
			continue
		}
		support, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id})
		if !ok || support.RoutineKind != catalog.FunctionRoutine || support.Builtin != nil && support.Builtin.ReturnsSet {
			return invalid()
		}
		args := []sqltype.Usage{state.Usage(), state.Usage()}
		result := state
		if slot == 3 {
			args = args[:1]
			result, _ = v.LookupType(sqltype.Bytea)
		} else if slot == 4 {
			bytea, _ := v.LookupType(sqltype.Bytea)
			args[0] = bytea.Usage()
		}
		formals, returned, err := AggregateSupportTypes(v, nil, support, d.Aggregate, args)
		if err != nil || returned.Type != result || slot == 2 && state.ID() == 2281 && support.Strict {
			return invalid()
		}
		for i, arg := range args {
			if arg.Type.ID() == 2281 && arg.Type == formals[i].Type {
				continue
			}
			if _, err := catalog.AggregateConversion(d.Aggregate.BinaryRecipe, arg, formals[i]); err != nil {
				return invalid()
			}
		}
		if state.ID() == 2281 && !aggregateInternalSupport(support, slot) {
			return invalid()
		}
	}
	if a.SortOperator != 0 {
		op, ok := v.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: a.SortOperator})
		if !ok || len(inputs) != 1 || op.Left != ids.OID(inputs[0].Type.ID()) || op.Right != op.Left || op.Function == 0 {
			return invalid()
		}
	}
	return nil
}
