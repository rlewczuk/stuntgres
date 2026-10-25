package engine

import (
	"context"
	"reflect"
	"slices"
	"testing"

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
					c := PayloadOf(s).Cursors.Entries["c"]
					root, catalog, actor := CursorRoot(c), CursorRoutineAuth(c).Catalog.State(), CursorRoutineAuth(c).State.CurrentUser
					if AuthorizationInputs(CursorRoutineAuthRef(c)) != &EngineOf(s).Authority || catalog == nil || CursorAuthority(c).Host != nil {
						t.Fatal("DECLARE did not detach its original routine authority")
					}
					original := slices.Clone(CursorRoutineDependencies(c))
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
					SetCursorRoutineDependencies(c, slices.Clone(original))
					switch mutation {
					case "relation_metadata":
						// Genuine same-transaction metadata publication changes a
						// base relation revision, not its captured input layout.
						sessionSQL(t, s, "ALTER TABLE unit_rows ENABLE ROW LEVEL SECURITY")
					case "missing":
						SetCursorRoutineDependencies(c, append(CursorRoutineDependencies(c)[:selected], CursorRoutineDependencies(c)[selected+1:]...))
					case "foreign":
						CursorRoutineDependencies(c)[selected].Ref = New().Catalog.Domain.Ref(CursorRoutineDependencies(c)[selected].Ref.Epoch, CursorRoutineDependencies(c)[selected].Ref.Address)
					case "epoch":
						CursorRoutineDependencies(c)[selected].Ref.Epoch++
					case "revision":
						CursorRoutineDependencies(c)[selected].Revision++
					}
					query := "FETCH 1 FROM c"
					if promotion {
						query = "COMMIT"
					}
					result, err := cursorUnitExecute(t, s, context.Background(), query)
					if mutation != "intact" && mutation != "relation_metadata" {
						if err == nil || result.Rows != nil || result.Count != 0 || c.Source != nil || AuthorizationLive(CursorRoutineAuth(c)) {
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
						if !CursorPromoted(c) || AuthorizationLive(CursorRoutineAuth(c)) || CursorRoutineDependencies(c) != nil || CursorRoot(c) != nil {
							t.Fatal("promotion retained declaration authority")
						}
					} else {
						if CursorRoot(c) != root || CursorRoutineAuth(c).Catalog.State() != catalog || CursorRoutineAuth(c).State.CurrentUser != actor || !reflect.DeepEqual(CursorRoutineDependencies(c), original) || CursorAuthority(c).Host != nil {
							t.Fatal("FETCH replaced original graph, authority or manifest")
						}
					}
					sessionSQL(t, s, "CLOSE c")
					if AuthorizationLive(CursorRoutineAuth(c)) || CursorRoutineDependencies(c) != nil {
						t.Fatal("CLOSE retained declaration authority")
					}
				})
			}
		})
	}
}
