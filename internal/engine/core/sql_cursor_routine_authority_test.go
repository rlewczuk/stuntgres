package core_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
)

// These exercise actual DECLARE capture and borrowed producer admission, not
// public SQL answer goldens or standalone expression authority.
func TestSQLCursorRoutineAuthorityLifetime(t *testing.T) {
	for _, promotion := range []bool{false, true} {
		name := "fetch"
		if promotion {
			name = "promotion"
		}
		t.Run(name, func(t *testing.T) {
			for _, mutation := range []string{"intact", "relation_metadata", "missing", "foreign", "epoch", "revision"} {
				t.Run(mutation, func(t *testing.T) {
					s := cursorUnitSetup(t)
					sessionSQL(t, s, "DECLARE c NO SCROLL CURSOR WITH HOLD FOR SELECT abs(i) FROM unit_rows")
					c := core.PayloadOf(s).Cursors.Entries["c"]
					root, catalog, actor := core.CursorRoot(c), core.CursorRoutineAuth(c).Catalog.State(), core.CursorRoutineAuth(c).State.CurrentUser
					if core.AuthorizationInputs(core.CursorRoutineAuthRef(c)) != &core.EngineOf(s).Authority || catalog == nil || core.CursorAuthority(c).Host != nil {
						t.Fatal("DECLARE did not detach its original routine authority")
					}
					original := slices.Clone(core.CursorRoutineDependencies(c))
					selected := -1
					for i, dependency := range original {
						if dependency.Ref.Address.ClassID == ids.ProcedureClassID {
							selected = i
							break
						}
					}
					if selected < 0 {
						t.Fatal("actual declaration lacks selected routine manifest")
					}
					core.SetCursorRoutineDependencies(c, slices.Clone(original))
					switch mutation {
					case "relation_metadata":
						// Genuine same-transaction metadata publication changes a
						// base relation revision, not its captured input layout.
						sessionSQL(t, s, "ALTER TABLE unit_rows ENABLE ROW LEVEL SECURITY")
					case "missing":
						core.SetCursorRoutineDependencies(c, append(core.CursorRoutineDependencies(c)[:selected], core.CursorRoutineDependencies(c)[selected+1:]...))
					case "foreign":
						core.CursorRoutineDependencies(c)[selected].Ref = engine.New().Catalog.Domain.Ref(core.CursorRoutineDependencies(c)[selected].Ref.Epoch, core.CursorRoutineDependencies(c)[selected].Ref.Address)
					case "epoch":
						core.CursorRoutineDependencies(c)[selected].Ref.Epoch++
					case "revision":
						core.CursorRoutineDependencies(c)[selected].Revision++
					}
					query := "FETCH 1 FROM c"
					if promotion {
						query = "COMMIT"
					}
					result, err := cursorUnitExecute(t, s, context.Background(), query)
					if mutation != "intact" && mutation != "relation_metadata" {
						if err == nil || result.Rows != nil || result.Count != 0 || c.Source != nil || core.AuthorizationLive(core.CursorRoutineAuth(c)) {
							t.Fatal("invalid original manifest survived producer admission", result, err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if result.Rows != nil {
						result.Rows.Close()
					}
					if promotion {
						if !core.CursorPromoted(c) || core.AuthorizationLive(core.CursorRoutineAuth(c)) || core.CursorRoutineDependencies(c) != nil || core.CursorRoot(c) != nil {
							t.Fatal("promotion retained declaration authority")
						}
					} else {
						if core.CursorRoot(c) != root || core.CursorRoutineAuth(c).Catalog.State() != catalog || core.CursorRoutineAuth(c).State.CurrentUser != actor || !reflect.DeepEqual(core.CursorRoutineDependencies(c), original) || core.CursorAuthority(c).Host != nil {
							t.Fatal("FETCH replaced original graph, authority or manifest")
						}
					}
					sessionSQL(t, s, "CLOSE c")
					if core.AuthorizationLive(core.CursorRoutineAuth(c)) || core.CursorRoutineDependencies(c) != nil {
						t.Fatal("CLOSE retained declaration authority")
					}
				})
			}
		})
	}
}
