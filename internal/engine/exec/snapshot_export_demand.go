package exec

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// RoutineExportRootDemand follows selected immutable descriptors rather than
// routine spelling or volatility. Opaque executable bodies can borrow their
// caller's snapshot, so conservatively retain roots before invoking them.
func RoutineExportRootDemand(read catalog.Read, routine *catalog.Procedure) bool {
	var stack [16]*catalog.Procedure
	return routineExportRootDemand(read, routine, stack[:0])
}

func routineExportRootDemand(read catalog.Read, routine *catalog.Procedure, stack []*catalog.Procedure) bool {
	if routine == nil {
		return false
	}
	for _, active := range stack {
		if active == routine {
			return false
		}
	}
	if routineOwnExportRootDemand(read, routine, stack) {
		return true
	}
	found := false
	_ = WalkRoutineBody(routine, nil, func(child *catalog.Procedure) error {
		found = found || routineOwnExportRootDemand(read, child, stack)
		return nil
	})
	return found
}

func routineOwnExportRootDemand(read catalog.Read, routine *catalog.Procedure, stack []*catalog.Procedure) bool {
	if routine.Builtin != nil && routine.Address().ObjectID == 3809 {
		return true
	}
	if routine.SQL == nil && routine.Host == nil && routine.HostHandler == nil && routine.PLHandler == catalog.NoPLHandler {
		return false
	}
	body, known := routine.SQLBody.(interface{ ExportRootDemand() bool })
	if !known || body.ExportRootDemand() {
		return true
	}
	stack = append(stack, routine)
	for _, dependency := range routine.SQLBody.Dependencies() {
		if child, ok := read.Procedure(dependency.Ref.Address); ok && routineExportRootDemand(read, child, stack) {
			return true
		}
	}
	return false
}

func (p *RoutineProgram) ExportRootDemand(read catalog.Read) bool {
	for _, call := range p.Calls {
		if call.defaultCall != nil {
			if acceptedDefaultCallExportRootDemand(read, call.defaultCall) {
				return true
			}
		} else if RoutineExportRootDemand(read, call.Routine) {
			return true
		}
	}
	return false
}

// AcceptedDefaultExpressionExportRootDemand is a retention-only projection of
// the existing source-owner selection. It neither activates a call nor grants
// invocation rights. Invalid evidence conservatively retains possible roots;
// the incumbent preflight/admission stage still reports its original error.
func AcceptedDefaultExpressionExportRootDemand(read catalog.Read, node expr.Expr) (demand, handled bool) {
	switch n := node.(type) {
	case *acceptedDefaultRoot:
		if n == nil || n.self != n || !n.artifact.Valid() || n.artifact.root != n ||
			preparation.AcceptedDefaultSource(read, n.artifact) != nil {
			return true, true
		}
		return false, true // The ordinary walker still visits its children.
	case *acceptedDefaultCall:
		return acceptedDefaultCallExportRootDemand(read, n), true
	default:
		return false, false
	}
}

func acceptedDefaultCallExportRootDemand(read catalog.Read, n *acceptedDefaultCall) bool {
	occurrence := AcceptedDefaultCall{node: n}
	prepared, ok := occurrence.prepared()
	if !ok {
		return true
	}
	current, err := preparation.AcceptedDefaultRoutine(read, n.artifact, occurrence)
	if err != nil || current == nil {
		return true
	}
	switch n.mode {
	case defaultPredecessor:
		return RoutineExportRootDemand(read, prepared.Procedure)
	case defaultCurrentPL:
		if !currentPLDefault(current) {
			return true
		}
		return RoutineExportRootDemand(read, current)
	default:
		return true
	}
}

// ExportRootDemand borrows only this accepted owner's immutable prepared
// occurrences. The caller independently fences its transaction and generation.
// Nothing from classification is retained in a recipe or reused as authority.
func (s *AcceptedDefaultSet) ExportRootDemand(read catalog.Read) bool {
	if s == nil {
		return false
	}
	if !s.Live() {
		return true
	}
	for _, artifact := range s.entries {
		if !artifact.Valid() || artifact.owner != s || acceptedDefaultTreeExportRootDemand(read, artifact.Expression()) {
			return true
		}
	}
	return false
}

func acceptedDefaultTreeExportRootDemand(read catalog.Read, node expr.Expr) bool {
	if node == nil {
		return false
	}
	if demand, handled := AcceptedDefaultExpressionExportRootDemand(read, node); handled {
		if demand {
			return true
		}
	} else {
		if demand, ok := node.(expr.ExportRootDemander); ok && demand.ExportRootDemand() {
			return true
		}
		if call, ok := node.(interface{ CallRoutineReference() expr.RoutineReference }); ok {
			if reference := call.CallRoutineReference(); reference != nil {
				if routine, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(reference.RoutineOID())}); ok &&
					RoutineExportRootDemand(read, routine) {
					return true
				}
			}
		}
	}
	if composite, ok := node.(expr.Composite); ok {
		for _, child := range composite.Children() {
			if acceptedDefaultTreeExportRootDemand(read, child) {
				return true
			}
		}
	}
	return false
}
