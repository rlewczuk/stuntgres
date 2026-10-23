package catalog

import (
	"iter"
	"strconv"
	"strings"

	"stuntgres/internal/engine/ids"
)

// OwnedAggregateDependencies exposes real support/type edges, not intagg's
// extension-owned helper aliases. Consumers own namespace and duplicate edges.
func OwnedAggregateDependencies(d *Procedure) iter.Seq[ids.ObjectAddress] {
	return func(yield func(ids.ObjectAddress) bool) {
		if d == nil || !d.DynamicCallable() || d.Aggregate == nil {
			return
		}
		if !yield(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Aggregate.Definition.Functions[0]}) {
			return
		}
		if !yield(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.Aggregate.Definition.TransitionType}) {
			return
		}
		if d.Signature == "" {
			return
		}
		for part := range strings.SplitSeq(string(d.Signature), ",") {
			id, err := strconv.ParseUint(part, 10, 32)
			if err != nil {
				return
			}
			if !yield(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}) {
				return
			}
		}
	}
}
