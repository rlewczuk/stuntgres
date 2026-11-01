package extension_test

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
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These witnesses retain actual protocol definitions, accepted fold objects and
// private portal graphs. SQL outcomes belong to the shared reference scenarios.
func TestIsnExpressionRetention(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		slot, inputs int
	}{
		{"comparison", "COALESCE(('9781234567897'::ean13 = '123456789X'::isbn),false)", extension.IsnComparisonStart + 8*6, 2},
		{"conversion", "COALESCE(isbn('9781234567897!'::ean13),'123456789X'::isbn)", extension.IsnConversionStart, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			_, err := e.Execute(op, &parser.CreateExtension{Name: parser.Identifier{Text: "isn"}}, 0, nil)
			done()
			if err != nil {
				t.Fatal(err)
			}
			view := e.CatalogRead()
			ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "isn"}))
			procedure := view.MustProcedure(ext.Installation.Members[tc.slot])
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "BEGIN")
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
			registry := core.PayloadOf(s).PreparedSQL
			command(func(scope *core.StatementScope) error {
				_, err := scope.ParseProtocol("isn_owned", "SELECT "+tc.body+" AS v", nil)
				return err
			})
			definition := registry.Lookup("isn_owned")
			if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
				t.Fatal("missing actual definition")
			}
			owner := definition.Description.Folds
			if owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
				t.Fatal("parse evaluated immutable values")
			}
			bind := func(name string) {
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal(name, "isn_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
			}
			bind("isn_first")
			first := registry.Binding("isn_first")
			accepted := owner.Current()
			if first == nil || first.Resource == nil || first.Resource.Root == nil || first.Folds == nil || len(accepted.Entries) == 0 || &first.Folds.Entries[0] != &accepted.Entries[0] {
				t.Fatal("missing accepted binding backing")
			}
			index := -1
			for i, entry := range accepted.Entries {
				if entry.Callable == procedure {
					index = i
					break
				}
			}
			if index < 0 {
				t.Fatal("missing original ISN procedure in retained fold")
			}
			retained := accepted.Entries[index]
			value := retained.Value
			if len(retained.Inputs) != tc.inputs || value.Type() != retained.Result.Type {
				t.Fatal("input/result generation")
			}
			for _, input := range retained.Inputs {
				if !input.Usage.Type.IsISN() || input.Value.Type() != input.Usage.Type || input.Value.IsNull() {
					t.Fatal("missing exact ISN input generation")
				}
			}
			firstRoot := first.Resource.Root
			bind("isn_second")
			second := registry.Binding("isn_second")
			if second == nil || second.Resource == nil || second.Resource.Root == nil || second.Resource.Root == firstRoot {
				t.Fatal("portal mutable graph ownership")
			}
			secondRoot := second.Resource.Root
			assertRetained := func() {
				t.Helper()
				current := owner.Current()
				if current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] || !enginetest.SamePublishedFold(current.Entries[index], retained) {
					t.Fatal("definition replaced retained artifact")
				}
				for _, binding := range []*core.PreparedBinding{first, second} {
					if binding.Entry.Description.Folds != owner || binding.Folds.Generation != accepted.Generation || &binding.Folds.Entries[0] != &accepted.Entries[0] || binding.Folds.Entries[index].Value != value || &binding.Folds.Entries[index].Inputs[0] != &retained.Inputs[0] {
						t.Fatal("accepted generation/input backing changed")
					}
					backer := false
					for _, dep := range binding.Entry.Description.Analysis.Dependencies {
						backer = backer || dep.Ref.Address == procedure.Address() && dep.Revision == procedure.Revision()
					}
					if !backer {
						t.Fatal("lost real backer dependency")
					}
				}
				if first.Resource.Root != firstRoot || second.Resource.Root != secondRoot {
					t.Fatal("accepted graph replaced")
				}
			}
			assertRetained()
			for _, cause := range []string{"cancellation", "retirement", "generation"} {
				t.Run(cause, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					operation, err := s.Acquire(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer operation.Done()
					scope, err := e.StartStatement(operation)
					if err != nil {
						t.Fatal(err)
					}
					local := enginetest.CloneFoldDescription(t, definition.Description)
					before := enginetest.SnapshotFoldDescription(local)
					scope.Expected = &local
					e.Mu.Lock()
					auth, err := e.AuthorizationLocked(operation)
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
						t.Fatal(err)
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
					err = publication.Accept(operation.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
					e.Mu.Unlock()
					_ = scope.Finish(err)
					if cause == "cancellation" && !errors.Is(err, context.Canceled) || cause != "cancellation" && !errors.Is(err, ids.ErrCatalogReplaced) || publication.Group.Sealed {
						t.Fatal("rejected publication accepted", err)
					}
					rejected := local.Folds.Current()
					if rejected.Generation != fenced.Generation || &rejected.Entries[0] != &before.Accepted.Entries[0] || rejected.Entries[index].Value != value {
						t.Fatal("rejected publication changed owner")
					}
					assertRetained()
				})
			}
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("isn_owned") })
			if owner.Live() || first.Retired || second.Retired || first.Folds.Entries[index].Value != value || second.Folds.Entries[index].Value != value {
				t.Fatal("retirement changed accepted artifacts")
			}
			for _, name := range []string{"isn_first", "isn_second"} {
				command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
			}
			if !first.Retired || !second.Retired {
				t.Fatal("portal lifetime leaked")
			}
			operation, _, err := s.AcquireCommand(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			err = e.SyncProtocolCycle(operation, cycle)
			operation.Done()
			if err != nil {
				t.Fatal(err)
			}
			enginetest.SessionSQL(t, s, "ROLLBACK")
		})
	}
}
