package stored

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
)

func testRoutineAggregateMovingCaptures(t *testing.T) {
	for _, window := range []bool{false, true} {
		read, p, provider := routineAggregateCaptureFixture(t, false, false)
		inverse := *provider.Support
		inverse.ObjectAddress.ObjectID = 49001
		inverse.ObjectName = "capture_inverse"
		read.State().SetDescriptor(inverse.Address(), &inverse)
		provider.InverseSupport = &inverse
		inverseCall := provider.Call.(plan.ArithmeticExpr)
		inverseCall.RoutineReference = &inverse
		provider.InverseCall = inverseCall
		root := p.actions[0].presentation.query.Root.(*plan.ProjectNode)
		if window {
			call := root.Child.(*plan.AggregateNode).Calls[0]
			root.Child = &plan.WindowNode{Child: &plan.SingletonNode{}, Calls: []plan.WindowCall{{Provider: provider, Procedure: call.Procedure, Routine: call.Ref, Arguments: call.Arguments}}}
		}
		capture := catalog.ExpressionDependency{
			Ref:      read.Owner().Domain.Ref(read.Epoch(), inverse.Address()),
			Revision: inverse.Revision(),
			Purpose:  catalog.AggregateSupportDependency,
		}
		dependencies := append(slices.Clone(p.actions[0].binding.Dependencies), capture)
		if err := validateRoutineCapture(p.actions[0], dependencies, read); err != nil {
			t.Fatalf("window=%t original moving support rejected: %v", window, err)
		}
		if err := validateRoutineCapture(p.actions[0], dependencies[:len(dependencies)-1], read); err == nil {
			t.Fatalf("window=%t missing inverse support accepted", window)
		}
		dependencies[len(dependencies)-1].Purpose = catalog.DefinitionDependency
		if err := validateRoutineCapture(p.actions[0], dependencies, read); err == nil {
			t.Fatalf("window=%t direct capture authorized inverse support", window)
		}
		dependencies[len(dependencies)-1] = capture
		dependencies[len(dependencies)-1].Revision++
		if err := validateRoutineCapture(p.actions[0], dependencies, read); err == nil {
			t.Fatalf("window=%t wrong original inverse revision accepted", window)
		}
		dependencies[len(dependencies)-1] = capture
		if window {
			call := &root.Child.(*plan.WindowNode).Calls[0]
			call.Provider = nil
			if err := validateRoutineCapture(p.actions[0], p.actions[0].binding.Dependencies[:1], read); err == nil {
				t.Fatal("unfinalized owned window accepted without support captures")
			}
			call.Provider = provider
		}
		provider.InverseCall = provider.Call
		provider.InverseSupport = provider.Support
		if err := validateRoutineCapture(p.actions[0], dependencies, read); err == nil {
			t.Fatalf("window=%t unattached inverse capture accepted", window)
		}
	}
}
