package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// These are ownership assertions on actual protocol definitions and accepted
// bindings, not another SQL-result fixture. Public outcomes live in reference/.
func TestHstoreComparisonPreparedOwnership(t *testing.T) {
	for _, tc := range []struct {
        name, expression, want string
        member, operator int
        result sqltype.ID
    }{
        {"equal", "hstore_eq('a=>1'::hstore,'a=>1'::hstore)", "t", extension.HstoreComparisonProcedureStart, 0, sqltype.Bool},
        {"compare", "hstore_cmp('a=>bc'::hstore,'ab=>c'::hstore)", "-1", extension.HstoreComparisonProcedureStart+6, 0, sqltype.Int4},
        {"hash", "hstore_hash(''::hstore)", "-1524351049", extension.HstoreComparisonProcedureStart+7, 0, sqltype.Int4},
        {"extended", "hstore_hash_extended(''::hstore,1::bigint)", "3044001184559810835", extension.HstoreComparisonProcedureStart+8, 0, sqltype.Int8},
        {"support_order", "'a=>bc'::hstore #<# 'ab=>c'::hstore", "t", extension.HstoreComparisonProcedureStart+2, extension.HstoreComparisonOperatorStart+2, sqltype.Bool},
    } {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE EXTENSION hstore")
			view := e.CatalogRead()
			installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
			procedure := view.MustProcedure(installation.Members[tc.member])
			query := "SELECT " + tc.expression + " AS v"
			enginetest.SessionSQL(t, s, "BEGIN")
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
			command(func(scope *core.StatementScope) error {
				_, err := scope.ParseProtocol("hs_owned", query, nil)
				return err
			})
			registry := core.PayloadOf(s).PreparedSQL
			definition := registry.Lookup("hs_owned")
			if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
				t.Fatal("missing real definition owner")
			}
			owner := definition.Description.Folds
			if len(owner.Current().Entries) != 0 || owner.Current().Generation != 0 {
				t.Fatal("Parse published evaluated values")
			}
			bind := func(name string) {
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal(name, "hs_owned", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
			}
			bind("hs_first")
			first := registry.Binding("hs_first")
			accepted := owner.Current()
			if first == nil || first.Folds == nil || first.Entry.Description.Folds != owner || len(accepted.Entries) == 0 || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] {
				t.Fatal("binding lost accepted definition ownership")
			}
			index := -1
			for i, entry := range accepted.Entries {
				if entry.Result.Type.ID() != tc.result {
					continue
				}
				if entry.Callable != procedure || entry.Operator != (ids.ObjectRef{}) {
					continue
				}
				index = i
				break
			}
			if index < 0 {
				t.Fatal("conversion has no selected backing-procedure fold capture")
			}
			retained := accepted.Entries[index]
			value := retained.Value
			if value.Type() != retained.Result.Type || value.Type() != definition.Description.Results[0].Usage.Type {
				t.Fatal("retained datum lost shape or exact descriptor ownership")
			}
			output, err := value.Type().Output(context.Background(), value)
			if err != nil || output != tc.want {
				t.Fatal("retained typed payload", output, err)
			}
			// Preflight folds the selected backing procedure before visiting its
			// operator wrapper. The datum belongs to that callable capture; the
			// unchanged operator remains a dependency and an actual graph node.
			assertGraph := func(binding *core.PreparedBinding) {
				t.Helper()
				if binding.Resource == nil || binding.Resource.Root == nil || len(binding.Resource.Root.Expressions) != 1 {
					t.Fatal("accepted binding lost its actual private query graph")
				}
				expression := binding.Resource.Root.Expressions[0]
				if tc.operator != 0 {
					selected, ok := expression.(plan.SelectedOperatorExpr)
					if !ok || selected.Operator == nil || selected.Ref.Address != installation.Members[tc.operator] || selected.Ref.Domain() != view.Owner().Domain || selected.Ref.Epoch != view.Epoch() || selected.Operator.Address() != selected.Ref.Address || selected.Operator.Function != procedure.Address().ObjectID || selected.RoutineReference != nil {
						t.Fatal("accepted prefix graph lost operator identity or duplicated callable admission")
					}
					analysis := binding.Entry.Description.Analysis
					if analysis == nil {
						t.Fatal("accepted prefix graph lost original analysis")
					}
					operatorDependency, backingDependency := false, false
					for _, dependency := range analysis.Dependencies {
						operatorDependency = operatorDependency || dependency.Ref == selected.Ref && dependency.Revision == selected.Operator.Revision()
						backingDependency = backingDependency || dependency.Ref.Address == procedure.Address() && dependency.Ref.Domain() == view.Owner().Domain && dependency.Ref.Epoch == view.Epoch() && dependency.Revision == procedure.Revision()
					}
					if !operatorDependency || !backingDependency {
						t.Fatal("prefix analysis omitted exact operator/backing authority")
					}
					expression = selected.Provider
				}
				literal, ok := expression.(plan.LiteralExpr)
				if !ok || literal.Value != value || literal.ResultUsage() != retained.Result || literal.Source != retained.Source || literal.Occurrence != retained.Occurrence || literal.RoutineReference != nil {
					t.Fatal("accepted graph does not consume exact immutable callable-fold backing/provenance")
				}
			}
			assertGraph(first)
			firstRoot := first.Resource.Root
			bind("hs_second")
			second := registry.Binding("hs_second")
			current := owner.Current()
			if second == nil || second == first || second.Entry.Description.Folds != owner || second.Folds.Generation != accepted.Generation || current.Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] || &current.Entries[0] != &accepted.Entries[0] || current.Entries[index].Value != value || !samePublishedFold(retained, current.Entries[index]) {
				t.Fatal("repeated Bind replaced immutable backing or capture provenance")
			}
			assertGraph(second)
			assertGraph(first)
			secondRoot := second.Resource.Root
			if secondRoot == firstRoot {
				t.Fatal("distinct accepted bindings shared a mutable resource graph")
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
					local := cloneFoldDescription(t, definition.Description)
					before := snapshotFoldDescription(local)
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
						t.Fatal("missing actual publication attempt", err)
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
						t.Fatal("publication fence accepted private replacement", cause, err)
					}
					rejected := local.Folds.Current()
					if rejected.Generation != fenced.Generation || &rejected.Entries[0] != &before.Accepted.Entries[0] || rejected.Entries[index].Value != value || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] || first.Folds.Generation != accepted.Generation || second.Folds.Generation != accepted.Generation || first.Folds.Entries[index].Value != value || second.Folds.Entries[index].Value != value {
						t.Fatal("rejected publication changed accepted owner, generation or immutable backing")
					}
					if first.Resource.Root != firstRoot || second.Resource.Root != secondRoot {
						t.Fatal("rejected publication replaced an accepted portal graph")
					}
					assertGraph(first)
					assertGraph(second)
				})
			}
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("hs_owned") })
			if owner.Live() || first.Retired || second.Retired || first.Folds.Entries[index].Value != value || second.Folds.Entries[index].Value != value {
				t.Fatal("definition retirement mutated accepted portals")
			}
			assertGraph(first)
			assertGraph(second)
			for _, name := range []string{"hs_first", "hs_second"} {
				command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
			}
			if !first.Retired || !second.Retired {
				t.Fatal("portal retirement leaked ownership")
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
