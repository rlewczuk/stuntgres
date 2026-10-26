package catalog

import (
	"iter"
	"strconv"
	"strings"

	"stuntgres/internal/engine/ids"
)

// OwnedAggregateDependencies exposes aggregate-owned support/type edges,
// deduplicated within this definition recording pass.
func OwnedAggregateDependencies(d *Procedure) iter.Seq[ids.ObjectAddress] {
	return func(yield func(ids.ObjectAddress) bool) {
		if d == nil || !d.DynamicCallable() || d.Aggregate == nil {
			return
		}
		slots := [...]int{0, 1, 5, 6, 7}
		for i, slot := range slots {
			if oid := d.Aggregate.Definition.Functions[slot]; oid != 0 {
				duplicate := false
				for _, previous := range slots[:i] {
					if d.Aggregate.Definition.Functions[previous] == oid {
						duplicate = true
						break
					}
				}
				if duplicate {
					continue
				}
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
		offset := 0
		for part := range strings.SplitSeq(string(d.Signature), ",") {
			id, err := strconv.ParseUint(part, 10, 32)
			if err != nil {
				return
			}
			duplicate := ids.OID(id) == d.Result
			for previous := range strings.SplitSeq(string(d.Signature)[:offset], ",") {
				duplicate = duplicate || previous == part
			}
			offset += len(part) + 1
			if !duplicate && !yield(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}) {
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
	if VisitProcedureDeclarationDependencies(d, yield) {
		VisitProcedureCapturedDefinitionDependencies(d, yield)
	}
}

// VisitProcedureCapturedDefinitionDependencies projects checked body/default
// captures onto direct definition edges. Each phase deduplicates independently;
// execution-only aggregate support captures never become caller edges.
// Declaration projection is separate so consumers can deduplicate declarations
// without collapsing independently recorded body/default rows.
func VisitProcedureCapturedDefinitionDependencies(d *Procedure, yield func(ids.ObjectAddress) bool) bool {
	if d.SQL == nil {
		return true
	}
	if d.SQLBody != nil {
		if !visitDirectExpressionDependencies(d.SQLBody.Dependencies(), yield) {
			return false
		}
	}
	if d.SQLDefaults != nil {
		if !visitDirectExpressionDependencies(d.SQLDefaults.Dependencies(), yield) {
			return false
		}
	}
	return true
}

// Body and defaults are separate recording passes. Historical captures can
// retain multiple stamps at one address, but each pass emits one semantic edge.
func visitDirectExpressionDependencies(dependencies []ExpressionDependency, yield func(ids.ObjectAddress) bool) bool {
	for i, dependency := range dependencies {
		if dependency.Purpose != DefinitionDependency && dependency.Purpose != OperandDependency {
			continue
		}
		duplicate := false
		for _, old := range dependencies[:i] {
			if (old.Purpose == DefinitionDependency || old.Purpose == OperandDependency) && old.Ref.Address == dependency.Ref.Address {
				duplicate = true
				break
			}
		}
		if !duplicate && !yield(dependency.Ref.Address) {
			return false
		}
	}
	return true
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
