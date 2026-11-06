package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSegIntervalExpressionOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, expression string
		member, operator int
	}{
		{"left", "'0 .. 1'::seg << '2 .. 3'::seg", 3, 11},
		{"right", "'2 .. 3'::seg >> '0 .. 1'::seg", 4, 12},
		{"over_left", "'0 .. 1'::seg &< '2 .. 3'::seg", 5, 13},
		{"over_right", "'2 .. 3'::seg &> '0 .. 1'::seg", 6, 14},
		{"same", "'1.0 .. 1.00'::seg = '1.0'::seg", 7, 15},
		{"overlap", "'0(+-)-1'::seg && '-2 .. 0'::seg", 8, 16},
		{"contains", "'0 .. 2'::seg @> '1'::seg", 9, 17},
		{"contained", "'1'::seg <@ '0 .. 2'::seg", 10, 18},
		{"backer", "seg_overlap('0 .. 1'::seg,'1 .. 2'::seg)", 8, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE EXTENSION seg")
			view := e.CatalogRead()
			installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "seg"})).Installation
			procedure := view.MustProcedure(installation.Members[tc.member])
			enginetest.SessionSQL(t, s, "BEGIN")
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
			command(func(scope *core.StatementScope) error {
				_, err := scope.ParseProtocol("si_owned", "SELECT "+tc.expression+" AS v", nil)
				return err
			})
			registry := core.PayloadOf(s).PreparedSQL
			definition := registry.Lookup("si_owned")
			if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
				t.Fatal("real definition owner missing")
			}
			owner := definition.Description.Folds
			if len(owner.Current().Entries) != 0 || owner.Current().Generation != 0 {
				t.Fatal("Parse evaluated retained values")
			}
			bind := func(name string) {
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal(name, "si_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
			}
			bind("si_first")
			first := registry.Binding("si_first")
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
			actual, err := value.Boolean()
			if err != nil || actual != (tc.name != "same") {
				t.Fatal("independent retained predicate", actual, err)
			}
			base := view.MustType(installation.Members[0]).SQLType
			if len(retained.Inputs) != 2 {
				t.Fatal("ordered seg interval operand captures missing")
			}
			leftInput, rightInput := retained.Inputs[0], retained.Inputs[1]
			for _, input := range []exec.FoldInput{leftInput, rightInput} {
				if input.Usage.Type != base || input.Value.Type() != base || input.Value.IsNull() {
					t.Fatal("seg interval operand lost installed generation")
				}
			}
			assertInputs := func(entry exec.FoldConstant) {
				t.Helper()
				if len(entry.Inputs) != 2 || &entry.Inputs[0] != &retained.Inputs[0] || entry.Inputs[0] != leftInput || entry.Inputs[1] != rightInput || entry.Callable != procedure {
					t.Fatal("accepted seg interval operand backing or original declaration changed")
				}
			}
			assertInputs(retained)
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
			bind("si_second")
			second := registry.Binding("si_second")
			current := owner.Current()
			if second == nil || second == first || second.Resource.Root == firstRoot || second.Entry.Description.Folds != owner || second.Folds.Generation != accepted.Generation || current.Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] || &current.Entries[0] != &accepted.Entries[0] || !enginetest.SamePublishedFold(retained, current.Entries[index]) {
				t.Fatal("Bind replaced immutable backing or provenance")
			}
			assertGraph(second)
			assertInputs(first.Folds.Entries[index])
			assertInputs(second.Folds.Entries[index])
			assertInputs(current.Entries[index])
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
			t.Run("effective_codec_generation", func(t *testing.T) {
				op, err := s.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				e.Mu.Lock()
				defer e.Mu.Unlock()
				auth, err := e.AuthorizationLocked(op)
				if err != nil {
					t.Fatal(err)
				}
				environment, err := e.PreparedEnvironmentLocked(auth, true)
				if err != nil {
					t.Fatal(err)
				}
				baseline := definition.Description.Analysis
				typ := auth.Catalog.MustType(installation.Members[0])
				successor := *typ
				successor.ObjectRevision = typ.Revision() + 1
				state := catalog.CloneState(auth.Catalog.State())
				state.SetDescriptor(successor.Address(), &successor)
				current := e.Catalog.Read(state)
				if successor.Revision() == typ.Revision() || successor.SQLType != typ.SQLType || !baseline.Valid(current, environment) || !baseline.FoldsValid(current, environment) || !core.FoldDependenciesValid(current, environment, accepted.Dependencies) {
					t.Fatal("installed seg presentation change lost unchanged executable authority")
				}
				// Nominal analysis can recognize an equivalent installed seg codec,
				// while the accepted typed kernel still owns its original pointer.
				segCodec := *typ.SQLType
				successor.SQLType = &segCodec
				state = catalog.CloneState(auth.Catalog.State())
				state.SetDescriptor(successor.Address(), &successor)
				current = e.Catalog.Read(state)
				if !baseline.Valid(current, environment) || !baseline.FoldsValid(current, environment) {
					t.Fatal("nominally equivalent seg codec changed declaration semantics")
				}
				if core.FoldDependenciesValid(current, environment, accepted.Dependencies) {
					t.Fatal("equivalent new seg pointer authorized old captured kernel")
				}
				if publication, err := core.BeginFoldPublication(&first.Entry.Description, first.Entry.Body, environment, current, first); !errors.Is(err, ids.ErrCatalogReplaced) || publication != nil {
					t.Fatal("old binding admitted a different installed codec pointer", err)
				}
				boolean := auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Bool)})
				demanded, captured := false, false
				for _, dependency := range baseline.Dependencies {
					if dependency.Ref.Address == boolean.Address() && dependency.Purpose != catalog.OperandDependency && dependency.Revision == core.DefinitionStamp(boolean, environment) {
						demanded = true
					}
				}
				for _, dependency := range accepted.Dependencies {
					if dependency.Ref.Address == boolean.Address() && dependency.Descriptor == boolean && dependency.Stamp == core.DefinitionStamp(boolean, environment) {
						captured = true
					}
				}
				if !demanded || !captured || baseline.FoldTypes[boolean.Address()] != boolean.SQLType {
					t.Fatal("actual accepted predicate lacks demanded BOOL codec authority")
				}
				result := *boolean
				result.ObjectRevision = boolean.Revision() + 1
				state = catalog.CloneState(auth.Catalog.State())
				state.SetDescriptor(result.Address(), &result)
				current = e.Catalog.Read(state)
				if result.Revision() == boolean.Revision() || baseline.Valid(current, environment) || !baseline.FoldsValid(current, environment) || !core.FoldDependenciesValid(current, environment, accepted.Dependencies) {
					t.Fatal("BOOL presentation revision confused analysis and executable identity")
				}
				codec := *boolean.SQLType
				result.SQLType = &codec
				state = catalog.CloneState(auth.Catalog.State())
				state.SetDescriptor(result.Address(), &result)
				current = e.Catalog.Read(state)
				if baseline.FoldsValid(current, environment) || core.FoldDependenciesValid(current, environment, accepted.Dependencies) {
					t.Fatal("replacement demanded BOOL codec retained old executable authority")
				}
				if publication, err := core.BeginFoldPublication(&first.Entry.Description, first.Entry.Body, environment, current, first); !errors.Is(err, ids.ErrCatalogReplaced) || publication != nil {
					t.Fatal("old accepted binding admitted replacement BOOL codec", err)
				}
				if baseline.FoldCallables[procedure.Address()] != procedure {
					t.Fatal("semantic probe lacks actual selected callable dependency")
				}
				revised := *procedure
				revised.ObjectRevision = procedure.Revision() + 1
				state = catalog.CloneState(auth.Catalog.State())
				state.SetDescriptor(revised.Address(), &revised)
				current = e.Catalog.Read(state)
				if revised.Revision() == procedure.Revision() || !exec.SameRoutineDeclaration(&revised, procedure) || baseline.Valid(current, environment) || !baseline.FoldsValid(current, environment) || !core.FoldDependenciesValid(current, environment, accepted.Dependencies) {
					t.Fatal("routine presentation revision replaced unchanged declaration")
				}
				revised.Volatility = expr.Volatile
				if exec.SameRoutineDeclaration(&revised, procedure) || baseline.FoldsValid(current, environment) || core.FoldDependenciesValid(current, environment, accepted.Dependencies) {
					t.Fatal("changed routine semantics retained accepted executable authority")
				}
				if publication, err := core.BeginFoldPublication(&first.Entry.Description, first.Entry.Body, environment, current, first); !errors.Is(err, ids.ErrCatalogReplaced) || publication != nil {
					t.Fatal("old accepted binding admitted changed routine semantics", err)
				}
				if definition.Description.Analysis != baseline || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] || !enginetest.SamePublishedFold(retained, accepted.Entries[index]) {
					t.Fatal("semantic probes mutated accepted definition or backing")
				}
				assertGraph(first)
				assertGraph(second)
				assertInputs(owner.Current().Entries[index])
			})
			for _, cause := range []string{"cancellation", "retirement", "generation"} {
				// Rejected publications finish with their actual error. Recover the
				// transaction explicitly without retiring portals accepted beforehand.
				enginetest.SessionSQL(t, s, "SAVEPOINT si_publication_probe")
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
					assertInputs(owner.Current().Entries[index])
					assertInputs(first.Folds.Entries[index])
					assertInputs(second.Folds.Entries[index])
				})
				enginetest.SessionSQL(t, s, "ROLLBACK TO SAVEPOINT si_publication_probe")
				enginetest.SessionSQL(t, s, "RELEASE SAVEPOINT si_publication_probe")
				assertGraph(first)
				assertGraph(second)
				assertInputs(owner.Current().Entries[index])
			}
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("si_owned") })
			if owner.Live() || first.Retired || second.Retired || first.Folds.Entries[index].Value != value || second.Folds.Entries[index].Value != value {
				t.Fatal("definition retirement changed accepted portals")
			}
			for _, name := range []string{"si_first", "si_second"} {
				count := 0
				command(func(scope *core.StatementScope) error {
					_, err := scope.ExecuteProtocolPortal(name, 0, func(fields []session.ProtocolField) error {
						count++
						want := "t"
						if tc.name == "same" {
							want = "f"
						}
						if len(fields) != 1 || fields[0].Null || string(fields[0].Bytes) != want {
							t.Fatal("accepted predicate execution", fields, want)
						}
						return nil
					}, nil, nil, core.CopyProtocolHooks{})
					return err
				})
				if count != 1 {
					t.Fatal("accepted source row count", count)
				}
			}
			// Execution can detach live producers; accepted immutable fold objects
			// and the separately retained graph must remain unchanged.
			if accepted.Entries[index].Value != value || !enginetest.SamePublishedFold(retained, accepted.Entries[index]) || firstRoot.Expressions[0] == nil || secondRoot.Expressions[0] == nil {
				t.Fatal("execution mutated saved accepted graph/folds")
			}
			for _, name := range []string{"si_first", "si_second"} {
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
