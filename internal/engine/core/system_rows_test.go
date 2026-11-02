package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestSystemRowsLifecycle(t *testing.T) {
	t.Run("stage_terminal_ownership", func(t *testing.T) {
		for _, canceled := range []bool{false, true} {
			e := engine.New()
			enginetest.Run(t, e, "CREATE EXTENSION tsm_system_rows")
			query, input := enginetest.QueryFixture(t, e, "SELECT * FROM t TABLESAMPLE system_rows(2)")
			project := query.Root.(*plan.ProjectNode)
			leaf := project.Child.(*plan.SourceNode)
			acceptedSample, acceptedCount := leaf.Sample, leaf.Sample.Count
			acceptedColumns := &leaf.QuerySchema[0]
			ctx, cancel := context.WithCancel(context.Background())
			failure, cleanup := errors.New("sampling child failure"), errors.New("sampling child cleanup")
			closes, evaluations := 0, 0
			inner := exec.Stage{Source: input, Columns: project.Schema(), Evaluation: exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, Finish: func() error { closes++; return cleanup }}
			inner.Expressions = append([]expr.Expr(nil), project.Expressions...)
			inner.Expressions[0] = enginetest.QueryProbe(project.Schema()[0].Usage, func(*exec.Context) (sqltype.Value, error) {
				evaluations++
				if canceled {
					cancel()
					return sqltype.Value{}, context.Canceled
				}
				return sqltype.Value{}, failure
			})
			bridge := &exec.Source{Ref: input.Ref, NestedRoot: project, NestedInputs: input, NestedStage: &inner, Materialized: true, FlatWidth: len(leaf.Schema())}
			rows, count, err := exec.Finalize(ctx, project, bridge, nil)
			cancel()
			want := failure
			if canceled {
				want = context.Canceled
			}
			if !errors.Is(err, want) || rows != nil || count != 0 || evaluations != 1 || closes != 1 {
				t.Fatal("primary error replaced or child not consumed/closed", rows, count, err, evaluations, closes)
			}
			if bridge.NestedStage != nil || input.View != nil || inner.Source != nil || inner.Selection != nil || inner.Expressions != nil || inner.Finish != nil || inner.Produce != nil || inner.Reset != nil {
				t.Fatal("failed sample retained graph, selection or pin")
			}
			if leaf.Sample != acceptedSample || !reflect.DeepEqual(leaf.Sample.Count, acceptedCount) || &leaf.QuerySchema[0] != acceptedColumns {
				t.Fatal("terminal cleanup mutated accepted plan backing")
			}
		}
	})
	t.Run("prepared_graph_and_reset", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, sql := range []string{"CREATE EXTENSION tsm_system_rows", "CREATE TABLE rows_owned(id int)", "INSERT INTO rows_owned VALUES(1),(2),(3)", "BEGIN"} {
			enginetest.SessionSQL(t, s, sql)
		}
		cycle := s.NewProtocolCycle()
		command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("rows_definition", "SELECT (point '0.10000000000000002,1')::text AS p,id FROM rows_owned TABLESAMPLE system_rows(2)", nil)
			return err
		})
		registry := core.PayloadOf(s).PreparedSQL
		definition := registry.Lookup("rows_definition")
		owner, analysis := definition.Description.Folds, definition.Description.Analysis
		bind := func(name string) *core.PreparedBinding {
			command(func(scope *core.StatementScope) error {
				_, err := scope.BindProtocolPortal(name, "rows_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
				return err
			})
			binding := registry.Binding(name)
			if binding == nil || binding.Resource == nil || binding.Resource.Root == nil || binding.Folds == nil {
				t.Fatal("binding lacks actual graph/group")
			}
			return binding
		}
		first, second := bind("rows_first"), bind("rows_second")
		firstResource, secondResource := first.Resource, second.Resource
		firstRoot, secondRoot := firstResource.Root, secondResource.Root
		firstFolds, secondFolds := *first.Folds, *second.Folds
		accepted := owner.Current()
		if firstRoot == secondRoot || firstResource.Closer == nil || secondResource.Closer == nil || firstResource.Closer == secondResource.Closer {
			t.Fatal("independent portals shared mutable producer")
		}
		var sample *plan.TableSample
		plan.VisitCTEQuery(firstRoot, func(node plan.Node) {
			if source, ok := node.(*plan.SourceNode); ok && source.Sample != nil {
				sample = source.Sample
			}
		}, func(expr.Expr) {}, true)
		if sample == nil {
			t.Fatal("prepared graph lost sampling")
		}
		count := sample.Count
		captured := false
		for _, dep := range analysis.Dependencies {
			if dep.Ref == sample.Handler {
				captured = dep.Purpose == catalog.DefinitionDependency && dep.Revision == sample.Procedure.Revision()
			}
		}
		if !captured {
			t.Fatal("handler absent from executable dependency manifest")
		}
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("rows_definition") })
		if owner.Live() || firstResource.Root != firstRoot || secondResource.Root != secondRoot {
			t.Fatal("definition retirement changed accepted portal graph")
		}
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("rows_first") })
		if firstResource.Root != nil || first.Folds != nil || !first.Retired || secondResource.Root != secondRoot {
			t.Fatal("portal close leaked or detached sibling ownership")
		}
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("rows_second") })
		if secondResource.Root != nil || second.Folds != nil || !second.Retired {
			t.Fatal("second portal retained live graph/group")
		}
		if !reflect.DeepEqual(sample.Count, count) || !reflect.DeepEqual(firstFolds.Entries, secondFolds.Entries) || !reflect.DeepEqual(firstFolds.Entries, accepted.Entries) {
			t.Fatal("close mutated immutable accepted backing")
		}
		enginetest.SessionSQL(t, s, "ROLLBACK")
		if err := engine.Reset(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(sample.Count, count) || firstRoot.Schema()[0].Usage.Type.ID() != sqltype.Text {
			t.Fatal("Reset mutated retained accepted graph/schema")
		}
	})
}
