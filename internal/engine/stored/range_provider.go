package stored

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

func validateRangeProvider(view catalog.Read, t *catalog.Type) error {
	r := t.SQLType.RangeType()
	if r == nil || !r.IsUserDefined() {
		return nil
	}
	d, ok := r.RangeDefinition()
	if !ok {
		return fmt.Errorf("stuntgres: missing custom range definition")
	}
	actual, ok := view.LookupType(d.Subtype.Type.ID())
	if !ok || actual != d.Subtype.Type {
		return fmt.Errorf("stuntgres: invalid range subtype generation")
	}
	if err := ValidateUsageCollation(view, d.Subtype); err != nil {
		return err
	}
	rangeOwner, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.ID())})
	if !ok || rangeOwner.SQLType != r {
		return fmt.Errorf("stuntgres: invalid range family generation")
	}
	multi := r.MultirangeType()
	if multi == nil || multi.RangeType() != r {
		return fmt.Errorf("stuntgres: invalid reciprocal multirange")
	}
	multiOwner, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(multi.ID())})
	if !ok || multiOwner.SQLType != multi || multiOwner.Owner != rangeOwner.Owner {
		return fmt.Errorf("stuntgres: invalid multirange generation")
	}
	if !authorization.MatchesDefaultBaseline(multiOwner.ACL, multiOwner.Owner, authorization.DefaultTypes, false) || !multiOwner.ACLSyntheticDefault {
		return fmt.Errorf("stuntgres: independently mutable multirange ACL")
	}
	class, ok := view.IndexClass(ids.OID(d.Class.OID()))
	if !ok || !class.SameIdentity(d.Class) || class.MethodOID() != 403 {
		return fmt.Errorf("stuntgres: invalid range subtype class")
	}
	class, err := routine.BindClassSupport(view, class)
	if err != nil {
		return err
	}
	if class.Support(1) != d.Class.Support(1) {
		return fmt.Errorf("stuntgres: stale range subtype support")
	}
	ordering, err := routine.BindIndexOrdering(view, d.Subtype, class)
	if err != nil {
		return err
	}
	if !ordering.SameOperation(d.Ordering) {
		return fmt.Errorf("stuntgres: invalid range ordering capture")
	}
	if d.Diff != 0 {
		p, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(d.Diff)})
		id := strconv.FormatUint(uint64(d.Subtype.Type.ID()), 10)
		if !ok || p.Result != 701 || p.Signature != catalog.ProcedureSignature(id+","+id) || p.Volatility != expr.Immutable || p.Builtin == nil && p.SQL == nil {
			return fmt.Errorf("stuntgres: invalid range subtype diff")
		}
	}
	return nil
}
