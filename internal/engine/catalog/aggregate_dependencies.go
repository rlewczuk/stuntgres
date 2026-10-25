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
		for _, slot := range [...]int{0, 1, 5, 6, 7} {
			if oid := d.Aggregate.Definition.Functions[slot]; oid != 0 {
				if !yield(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}) {
					return
				}
			}
		}
		// State types are retained through support signatures. Result and input
		// types are real procedure declaration dependencies in their own right.
		if !yield(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.Result}) {
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

// VisitProcedureDeclarationDependencies visits namespace, support and declared
// result/formal dependencies. It returns false when yield stops visitation.
// Checked body/default dependencies are separate capture passes.
func VisitProcedureDeclarationDependencies(d *Procedure, yield func(ids.ObjectAddress) bool) bool {
	if !yield(d.Namespace()) {
		return false
	}
	for address := range OwnedAggregateDependencies(d) {
		if !yield(address) {
			return false
		}
	}
	if d.SQL == nil {
		return true
	}
	if !yield(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.Result}) {
		return false
	}
	for _, formal := range d.SQL.Formals {
		if !yield(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(formal.Usage.Type.ID())}) {
			return false
		}
	}
	return true
}

// VisitProcedureDefinitionDependencies visits the ordinary hard definition
// edges shared by pg_depend, publication admission and retirement preflight.
// Quoted source and PL replay-order metadata are deliberately not hard edges.
// Provider-specific generated/extension edges remain with their providers.
func VisitProcedureDefinitionDependencies(d *Procedure, yield func(ids.ObjectAddress) bool) {
	if !VisitProcedureDeclarationDependencies(d, yield) || d.SQL == nil {
		return
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
