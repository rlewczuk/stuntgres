package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIntarraySetBinding(t *testing.T) {
	for _, tc := range []struct {
		name, expression string
		member, operator int
	}{
		{"overlap", "'{3,1,3,2}'::integer[] OPERATOR(public.&&) '{2,4,2}'::integer[]", 6, 22},
		{"contains", "'{3,1,3,2}'::integer[] OPERATOR(public.@>) '{3,3}'::integer[]", 7, 23},
		{"contained", "'{3,3}'::integer[] OPERATOR(public.<@) '{3,1,3,2}'::integer[]", 8, 24},
		{"append", "'{3,1,3,2}'::integer[] OPERATOR(public.+) 3", 9, 25},
		{"concat", "'{3,1,3,2}'::integer[] OPERATOR(public.+) '{2,4,2}'::integer[]", 10, 26},
		{"delete", "'{3,1,3,2}'::integer[] OPERATOR(public.-) 3", 11, 27},
		{"subtract", "'{3,1,3,2}'::integer[] OPERATOR(public.-) '{2}'::integer[]", 12, 28},
		{"union_elem", "'{3,1,3,2}'::integer[] OPERATOR(public.|) 3", 13, 29},
		{"union", "'{3,1,3,2}'::integer[] OPERATOR(public.|) '{2,4,2}'::integer[]", 14, 30},
		{"intersect", "public._int_inter('{3,1,3,2}'::integer[],'{2,4,2}'::integer[])", 15, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE EXTENSION intarray")
			view := e.CatalogRead()
			installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"})).Installation
			procedure := view.MustProcedure(installation.Members[tc.member])
			enginetest.SessionSQL(t, s, "BEGIN")
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
			command(func(scope *core.StatementScope) error {
				_, err := scope.ParseProtocol("ia_owned", "SELECT "+tc.expression+" AS v", nil)
				return err
			})
			registry := core.PayloadOf(s).PreparedSQL
			definition := registry.Lookup("ia_owned")
			if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
				t.Fatal("real definition owner missing")
			}
			owner := definition.Description.Folds
			if len(owner.Current().Entries) != 0 || owner.Current().Generation != 0 {
				t.Fatal("Parse evaluated retained values")
			}
			bind := func(name string) {
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal(name, "ia_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
			}
			bind("ia_first")
			first := registry.Binding("ia_first")
			accepted := owner.Current()
			if first == nil || first.Folds == nil || len(accepted.Entries) == 0 || &first.Folds.Entries[0] != &accepted.Entries[0] {
				t.Fatal("accepted definition backing lost")
			}
			index := -1
			for i, entry := range accepted.Entries {
				if entry.Callable == procedure && entry.Operator == (ids.ObjectRef{}) {
					index = i
					break
				}
			}
			if index < 0 {
				t.Fatal("selected backing procedure fold absent")
			}
			retained := accepted.Entries[index]
			value := retained.Value
			assertGraph := func(binding *core.PreparedBinding) {
				t.Helper()
				if binding.Resource == nil || binding.Resource.Root == nil || len(binding.Resource.Root.Expressions) != 1 {
					t.Fatal("actual private graph missing")
				}
				expression := binding.Resource.Root.Expressions[0]
				if tc.operator != 0 {
					selected, ok := expression.(plan.SelectedOperatorExpr)
					if !ok || selected.Ref.Address != installation.Members[tc.operator] || selected.Ref.Domain() != view.Owner().Domain || selected.RoutineReference != nil || selected.Operator.Function != procedure.Address().ObjectID {
						t.Fatal("operator identity/admission lost")
					}
					expression = selected.Provider
				}
				literal, ok := expression.(plan.LiteralExpr)
				if !ok || literal.Value != value || literal.ResultUsage() != retained.Result || literal.Source != retained.Source || literal.Occurrence != retained.Occurrence || literal.RoutineReference != nil {
					t.Fatal("graph replaced exact callable fold datum/provenance")
				}
			}
			assertGraph(first)
			firstRoot := first.Resource.Root
			bind("ia_second")
			second := registry.Binding("ia_second")
			current := owner.Current()
			if second == nil || second == first || second.Resource.Root == firstRoot || second.Entry.Description.Folds != owner || second.Folds.Generation != accepted.Generation || current.Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] || &current.Entries[0] != &accepted.Entries[0] || !enginetest.SamePublishedFold(retained, current.Entries[index]) {
				t.Fatal("Bind replaced immutable backing or provenance")
			}
			assertGraph(second)
			secondRoot := second.Resource.Root
			firstProducer := core.PortalProducer(core.ProtocolPortalOf(first))
			secondProducer := core.PortalProducer(core.ProtocolPortalOf(second))
			if firstProducer == nil || secondProducer == nil || firstProducer == secondProducer {
				t.Fatal("accepted portals share live producer")
			}
			firstState, secondState := core.CursorState(firstProducer), core.CursorState(secondProducer)
			firstContext := firstState.Evaluation(context.Background())
			secondContext := secondState.Evaluation(context.Background())
			if firstContext.Query == nil || secondContext.Query == nil || firstContext.Query == secondContext.Query || firstContext.Query.Source == secondContext.Query.Source {
				t.Fatal("accepted invocations share mutable evaluation frame")
			}
			for _, cause := range []string{"cancellation", "retirement", "generation"} {
				t.Run(cause, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					op, err := s.Acquire(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer op.Done()
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					local := enginetest.CloneFoldDescription(t, definition.Description)
					before := enginetest.SnapshotFoldDescription(local)
					scope.Expected = &local
					e.Mu.Lock()
					auth, err := e.AuthorizationLocked(op)
					if err != nil {
						e.Mu.Unlock()
						scope.Finish(err)
						t.Fatal(err)
					}
					environment, err := e.PreparedEnvironmentLocked(auth, true)
					if err != nil {
						e.Mu.Unlock()
						scope.Finish(err)
						t.Fatal(err)
					}
					publication, err := core.BeginFoldPublication(&local, definition.Body, environment, auth.Catalog, nil)
					if err != nil || publication == nil {
						e.Mu.Unlock()
						scope.Finish(err)
						t.Fatal("actual publication absent", err)
					}
					publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
					publication.Group.Private = true
					publication.Group.Entries[index].Value = sqltype.Null(retained.Result.Type)
					switch cause {
					case "cancellation":
						cancel()
					case "retirement":
						local.Retire()
					case "generation":
						local.Folds.Invalidate(true)
					}
					fenced := local.Folds.Current()
					err = publication.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
					e.Mu.Unlock()
					_ = scope.Finish(err)
					if cause == "cancellation" && !errors.Is(err, context.Canceled) || cause != "cancellation" && !errors.Is(err, ids.ErrCatalogReplaced) || publication.Group.Sealed {
						t.Fatal("publication fence accepted candidate", cause, err)
					}
					rejected := local.Folds.Current()
					if rejected.Generation != fenced.Generation || &rejected.Entries[0] != &before.Accepted.Entries[0] || rejected.Entries[index].Value != value || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] || first.Resource.Root != firstRoot || second.Resource.Root != secondRoot || first.Folds.Entries[index].Value != value || second.Folds.Entries[index].Value != value {
						t.Fatal("rejected publication changed accepted backing")
					}
					assertGraph(first)
					assertGraph(second)
				})
			}
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("ia_owned") })
			if owner.Live() || first.Retired || second.Retired || first.Folds.Entries[index].Value != value || second.Folds.Entries[index].Value != value {
				t.Fatal("definition retirement changed accepted portals")
			}
			for _, name := range []string{"ia_first", "ia_second"} {
				command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
			}
			op, _, err := s.AcquireCommand(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			err = e.SyncProtocolCycle(op, cycle)
			op.Done()
			if err != nil {
				t.Fatal(err)
			}
			enginetest.SessionSQL(t, s, "ROLLBACK")
		})
	}
}
