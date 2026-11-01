package views_test

import (
	"reflect"
	"slices"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"testing"
)

func TestPreparedFoldProtocolRoutineReplacement(t *testing.T) {
	for _, parameterized := range []bool{false, true} {
		t.Run(map[bool]string{false: "definition", true: "imported_frame"}[parameterized], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE FUNCTION portal_recipe() RETURNS integer RETURN 5", "CREATE TABLE portal_recipe_rows(v integer DEFAULT portal_recipe())", "INSERT INTO portal_recipe_rows DEFAULT VALUES", "SET extra_float_digits=3", "BEGIN"} {
				enginetest.SessionSQL(t, s, q)
			}
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
			query := "SELECT length((point '0.10000000000000002,1')::text),4::integer FROM portal_recipe_rows"
			if parameterized {
				query = "SELECT length((point '0.10000000000000002,1')::text),$1::integer FROM portal_recipe_rows"
			}
			command(func(scope *core.StatementScope) error {
				_, err := scope.ParseProtocol("recipe", query, nil)
				return err
			})
			bind := func(name string) {
				command(func(scope *core.StatementScope) error {
					data := []byte{0, 0, 0, 0, 0, 0}
					if parameterized {
						data = []byte{0, 0, 0, 1, 0, 0, 0, 1, '4', 0, 0}
					}
					_, err := scope.BindProtocolPortal(name, "recipe", &enginetest.ProtocolReader{Data: data})
					return err
				})
			}
			bind("old")
			registry := core.PayloadOf(s).PreparedSQL
			old := registry.Binding("old")
			analysis, resource, group := old.Entry.Description.Analysis, old.Resource, old.Folds
			if resource == nil || resource.Root == nil || group == nil || len(group.Entries) == 0 {
				t.Fatal("Bind omitted actual graph or nonempty folds")
			}
			graph := resource.Root
			accepted := *group
			entries := slices.Clone(group.Entries)
			dependencies := slices.Clone(analysis.Dependencies)
			enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
			enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION portal_recipe() RETURNS integer RETURN 9")
			// New planning belongs to the reusable definition, not the accepted portal.
			bind("new")
			fresh := registry.Binding("new")
			if fresh.Entry.Description.Analysis == analysis || fresh.Folds == group || &fresh.Folds.Entries[0] == &accepted.Entries[0] {
				t.Fatal("new Bind reused invalidated definition artifacts")
			}
			replacementOwner := registry.Lookup("recipe").Description.Folds
			replacementState := replacementOwner.Current()
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("recipe") })
			if replacementOwner.Live() {
				t.Fatal("definition retirement kept publication rights")
			}
			execute := func(name, want string) {
				rows := 0
				command(func(scope *core.StatementScope) error {
					_, err := scope.ExecuteProtocolPortal(name, 0, func(fields []session.ProtocolField) error {
						rows++
						if len(fields) != 2 || string(fields[0].Bytes) != want || string(fields[1].Bytes) != "4" {
							t.Fatalf("accepted graph/frame output %v wanted %s/4", fields, want)
						}
						return nil
					}, nil, nil, core.CopyProtocolHooks{})
					return err
				})
				if rows != 1 {
					t.Fatal("wrong accepted row multiplicity", rows)
				}
			}
			execute("old", "23")
			execute("new", "7")
			if old.Entry.Description.Analysis != analysis || old.Resource != resource || !slices.Equal(analysis.Dependencies, dependencies) || !reflect.DeepEqual(accepted.Entries, entries) || &group.Entries[0] != &accepted.Entries[0] || group.Generation != accepted.Generation {
				t.Fatal("Execute mutated saved accepted analysis/group/generation/backing")
			}
			// Terminal live-resource detachment is allowed; the saved graph and accepted
			// objects above remain the actual independently owned immutable artifacts.
			retained := replacementOwner.Current()
			if graph == nil || replacementOwner.Live() || retained.Generation != replacementState.Generation+1 || len(retained.Entries) != len(replacementState.Entries) {
				t.Fatal("old portal published into retired reusable definition")
			}
			if len(retained.Entries) != 0 && &retained.Entries[0] != &replacementState.Entries[0] {
				t.Fatal("retired definition backing changed")
			}
			enginetest.SessionSQL(t, s, "ROLLBACK")
		})
	}
}
