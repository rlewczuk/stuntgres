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
		if final := d.Aggregate.Definition.Functions[1]; final != 0 {
			if !yield(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: final}) {
				return
			}
		}
		if d.Result != d.Aggregate.Definition.TransitionType && !yield(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.Result}) {
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

// VisitProcedureDefinitionDependencies visits the ordinary hard definition
// edges shared by pg_depend, publication admission and retirement preflight.
// Quoted source and PL replay-order metadata are deliberately not hard edges.
// Provider-specific generated/extension edges remain with their providers.
func VisitProcedureDefinitionDependencies(d *Procedure, yield func(ids.ObjectAddress) bool) {
	if !yield(d.Namespace()) {
		return
	}
	for address := range OwnedAggregateDependencies(d) {
		if !yield(address) {
			return
		}
	}
	if d.SQL == nil {
		return
	}
	if !yield(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.Result}) {
		return
	}
	for _, formal := range d.SQL.Formals {
		if !yield(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(formal.Usage.Type.ID())}) {
			return
		}
	}
	if d.SQLBody != nil {
		for _, dependency := range d.SQLBody.Dependencies() {
			if !yield(dependency.Ref.Address) {
				return
			}
		}
	}
	if d.SQLDefaults != nil {
		for _, dependency := range d.SQLDefaults.Dependencies() {
			if !yield(dependency.Ref.Address) {
				return
			}
		}
	}
}

// ProcedureDefinitionDependsOn is allocation-free and stops at the first edge.
func ProcedureDefinitionDependsOn(d *Procedure, target ids.ObjectAddress) bool {
	if target.ObjectID == 0 {
		return false
	}
	found := false
	VisitProcedureDefinitionDependencies(d, func(address ids.ObjectAddress) bool {
		found = address == target
		return !found
	})
	return found
}
