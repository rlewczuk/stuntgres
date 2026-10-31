package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// The public regression proves real view/body execution and ACL transitions.
// Each slot must retain its own historical declaration, never another slot's
// declaration or a replacement from the current catalog.
type routineTestCapture struct {
	Ref       ids.ObjectRef
	Procedure *catalog.Procedure
}

func TestRoutineBuiltinCapturedDeclarations(t *testing.T) {
	for _, query := range []string{
		"SELECT bool_and(f),every(f),bool_and(f) FROM t",
		"SELECT row_number() OVER(ORDER BY id) FROM t",
		"SELECT DISTINCT id FROM t",
		"SELECT id FROM t GROUP BY id",
		"SELECT sum(v) OVER(PARTITION BY id) FROM t",
		"SELECT rank() OVER(ORDER BY id) FROM t",
	} {
		t.Run(query, func(t *testing.T) {
			e := engine.New()
			bound, source := enginetest.QueryFixture(t, e, query)
			defer source.Close()
			var captures []routineTestCapture
			plan.VisitExecutableQuery(bound.Root, func(node plan.Node) {
				switch n := node.(type) {
				case *plan.AggregateNode:
					if query == "SELECT bool_and(f),every(f),bool_and(f) FROM t" && len(n.Calls) != 2 {
						t.Fatal("distinct declarations did not retain independent slots", n.Calls)
					}
					for _, group := range n.Grouping {
						captures = append(captures, routineTestCapture{Ref: group.Routine, Procedure: group.Procedure})
					}
					for _, call := range n.Calls {
						captures = append(captures, routineTestCapture{Ref: call.Routine, Procedure: call.Procedure})
					}
				case *plan.WindowNode:
					for _, call := range n.Calls {
						captures = append(captures, routineTestCapture{Ref: call.Routine, Procedure: call.Procedure})
					}
					for _, spec := range n.Specs {
						for _, group := range spec.Grouping {
							captures = append(captures, routineTestCapture{Ref: group.Routine, Procedure: group.Procedure})
						}
						for _, key := range spec.Order {
							captures = append(captures, routineTestCapture{Ref: key.EqualityRoutine, Procedure: key.EqualityProcedure})
						}
					}
				case *plan.DistinctNode:
					for _, group := range n.Grouping {
						captures = append(captures, routineTestCapture{Ref: group.Routine, Procedure: group.Procedure})
					}
				}
			}, func(expr.Expr) {})
			if len(captures) == 0 {
				t.Fatal("no captured declarations")
			}
			base := e.CatalogRead()
			state := catalog.CloneState(base.State())
			var dependencies []catalog.ExpressionDependency
			for _, capture := range captures {
				p := capture.Procedure
				if p == nil || p.Builtin == nil || p.Address() != capture.Ref.Address || p != base.MustProcedure(capture.Ref.Address) {
					t.Fatal("producer did not retain its original checked declaration", capture)
				}
				dependencies = append(dependencies, catalog.ExpressionDependency{Ref: capture.Ref, Revision: p.Revision()})
				current := *p
				current.ObjectName = "captured_" + p.Name()
				current.ObjectRevision++
				state.UnbindName(catalog.DescriptorNameKey(p))
				state.SetDescriptor(current.Address(), &current)
				state.BindName(catalog.DescriptorNameKey(&current), current.Address())
				index := catalog.ProcedureIndexEdit{Index: state.ProcedureNames()}
				index.Replace(p, &current)
				state.SetProcedureNames(index.Index)
			}
			auth := enginetest.CatalogAuthorization(e, e.Catalog.Read(state))
			context := exec.Context{EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: dependencies}}
			for _, capture := range captures {
				if err := exec.AdmitRoutineReference(&context, capture.Ref, capture.Procedure, "function"); err != nil {
					t.Fatal("original checked occurrence rejected", err)
				}
				if err := exec.AdmitRoutineReference(&context, capture.Ref, nil, "function"); err == nil {
					t.Fatal("reference-only occurrence acquired historical declaration proof")
				}
				if err := exec.AdmitRoutineReference(&context, ids.ObjectRef{}, capture.Procedure, "function"); err == nil {
					t.Fatal("descriptor-only occurrence acquired reference proof")
				}
				withoutManifest := context
				withoutManifest.RoutineChecks = []catalog.ExpressionDependency{}
				if err := exec.AdmitRoutineReference(&withoutManifest, capture.Ref, capture.Procedure, "function"); err == nil {
					t.Fatal("occurrence without original manifest accepted")
				}
				current := state.MustProcedure(capture.Ref.Address)
				if err := exec.AdmitRoutineReference(&context, capture.Ref, current, "function"); err == nil {
					t.Fatal("current descriptor beside historical manifest accepted")
				}
				if err := exec.AdmitRoutineDescriptor(auth.AdmissionAccess(), dependencies, capture.Procedure, nil); err == nil {
					t.Fatal("direct source lost revision fence")
				}
				for _, other := range captures {
					if other.Ref != capture.Ref && other.Ref != (ids.ObjectRef{}) {
						if err := exec.AdmitRoutineReference(&context, capture.Ref, other.Procedure, "function"); err == nil {
							t.Fatal("different selected declarations interchangeable")
						}
					}
				}
			}
		})
	}
}
