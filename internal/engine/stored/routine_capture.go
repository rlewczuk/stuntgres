package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
)

// Validate against the retained executable, not an address-only semantic
// projection. In particular, one origin can never authorize the other origin.
func validateRoutineCapture(action RuleAction, dependencies []catalog.ExpressionDependency, read catalog.Read) error {
	var failure error
	var small [64]bool
	matched := small[:]
	if len(dependencies) > len(small) {
		matched = make([]bool, len(dependencies))
	}
	require := func(d *catalog.Procedure, ref ids.ObjectRef, purpose catalog.DependencyPurpose) {
		if failure != nil {
			return
		}
		if d == nil || d.Revision() == 0 {
			failure = fmt.Errorf("stuntgres: missing original routine descriptor")
			return
		}
		if read.State() != nil {
			if read.Owner() == nil || read.Owner().Principals == nil || d.Owner.ID() == 0 || d.Owner != read.Owner().Principals.Principal(d.Owner.ID()) {
				failure = fmt.Errorf("stuntgres: foreign original routine descriptor")
				return
			}
			current, ok := read.Procedure(d.Address())
			if !ok || !exec.SameRoutineDeclaration(current, d) {
				failure = fmt.Errorf("stuntgres: stale original routine descriptor")
				return
			}
		}
		found := false
		for i, dependency := range dependencies {
			if dependency.Ref.Address == d.Address() && dependency.Revision == d.Revision() && dependency.Purpose == purpose && (ref == (ids.ObjectRef{}) || ref == dependency.Ref) {
				matched[i] = true
				found = true
			}
		}
		if !found {
			failure = fmt.Errorf("stuntgres: missing original routine capture purpose")
		}
	}
	scalar := func(bound expr.Expr, purpose catalog.DependencyPurpose) {
		plan.WalkExpression(bound, func(node expr.Expr) {
			if failure != nil {
				return
			}
			d, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil {
				failure = err
				return
			}
			if d != nil {
				ref := ids.ObjectRef{}
				if call, ok := node.(plan.SQLRoutineExpr); ok {
					ref = call.Ref
				}
				require(d, ref, purpose)
				if failure == nil {
					failure = exec.WalkRoutineBody(d, node, func(body *catalog.Procedure) error { require(body, ids.ObjectRef{}, purpose); return failure })
				}
			}
			if conditional, ok := node.(plan.CaseExpr); ok {
				for _, arm := range conditional.Arms {
					if arm.Routine != nil {
						require(arm.Routine, ids.ObjectRef{}, purpose)
					}
				}
			}
		})
	}
	aggregate := func(provider *exec.TransitionAggregate, ref ids.ObjectRef) {
		require(provider.Aggregate, ref, catalog.DefinitionDependency)
		require(provider.Support, ids.ObjectRef{}, catalog.AggregateSupportDependency)
		if provider.InverseSupport != nil {
			require(provider.InverseSupport, ids.ObjectRef{}, catalog.AggregateSupportDependency)
		}
		if provider.Final != nil {
			require(provider.Final, ids.ObjectRef{}, catalog.AggregateSupportDependency)
		}
		for _, call := range [3]expr.Expr{provider.Call, provider.InverseCall, provider.FinalCall} {
			if call != nil {
				scalar(call, catalog.AggregateSupportDependency)
			}
		}
	}
	visit := func(presentation *View) {
		if presentation == nil || presentation.query == nil {
			failure = fmt.Errorf("stuntgres: missing routine capture presentation")
			return
		}
		// Alias datums are definition references, not invocations. The retained
		// presentation owns their original stamps; current descriptors and names
		// cannot supply that authority (including during zero-read remap).
		for _, literal := range presentation.literals {
			usage := literal.ResultUsage()
			if !usage.Valid() || literal.Value.Type() != usage.Type {
				failure = fmt.Errorf("stuntgres: invalid routine literal capture")
				return
			}
			_, err := regobj.RemapRoutineDatum(literal.Value, func(oid ids.OID) ids.OID {
				address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
				captured := false
				for _, original := range presentation.dependencies {
					if original.Ref.Address != address || original.Purpose != catalog.DefinitionDependency {
						continue
					}
					found := false
					for i, dependency := range dependencies {
						if dependency == original {
							matched[i] = true
							found = true
						}
					}
					if !found {
						failure = fmt.Errorf("stuntgres: missing original literal routine capture")
					}
					captured = true
				}
				if !captured {
					for _, dependency := range dependencies {
						if dependency.Ref.Address == address && dependency.Purpose == catalog.AggregateSupportDependency {
							failure = fmt.Errorf("stuntgres: missing original literal definition capture")
						}
					}
				}
				return oid
			})
			if err != nil {
				failure = err
				return
			}
		}
		plan.VisitCTEQuery(presentation.query.Root, func(node plan.Node) {
			switch node := node.(type) {
			case *plan.AggregateNode:
				for _, call := range node.Calls {
					if call.Procedure != nil {
						require(call.Procedure, call.Ref, catalog.DefinitionDependency)
					}
					if provider, ok := call.Provider.(*exec.TransitionAggregate); ok && provider != nil {
						aggregate(provider, call.Ref)
					} else if call.Procedure != nil && call.Procedure.Aggregate != nil {
						failure = fmt.Errorf("stuntgres: missing owned aggregate capture provider")
					}
				}
			case *plan.WindowNode:
				for _, call := range node.Calls {
					if call.Procedure != nil {
						require(call.Procedure, call.Routine, catalog.DefinitionDependency)
					}
					if provider, ok := call.Provider.(*exec.TransitionAggregate); ok && provider != nil {
						aggregate(provider, call.Routine)
					} else if call.Procedure != nil && call.Procedure.Aggregate != nil {
						failure = fmt.Errorf("stuntgres: missing owned window capture provider")
					}
				}
			}
		}, func(bound expr.Expr) { scalar(bound, catalog.DefinitionDependency) }, true)
	}
	visit(action.presentation)
	for _, presentation := range action.modifierPresentations {
		visit(presentation)
	}
	if failure != nil {
		return failure
	}
	for i, dependency := range dependencies {
		if !dependency.Purpose.Valid() || dependency.Revision == 0 {
			return fmt.Errorf("stuntgres: invalid original routine capture")
		}
		if dependency.Ref.Address.ClassID != ids.ProcedureClassID {
			continue
		}
		// Shared support addresses require an actual invocation or a checked
		// literal with its own original definition capture.
		supportAddress := dependency.Purpose == catalog.AggregateSupportDependency
		for _, other := range dependencies {
			supportAddress = supportAddress || other.Purpose == catalog.AggregateSupportDependency && other.Ref.Address == dependency.Ref.Address
		}
		if supportAddress && !matched[i] {
			return fmt.Errorf("stuntgres: extra routine capture purpose")
		}
	}
	return nil
}
