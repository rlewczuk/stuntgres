package engine

import (
	"context"
	"reflect"
	"slices"
	"testing"
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
					c := s.cursors.entries["c"]
					root, catalog, actor := c.root, c.routineAuth.catalog.state, c.routineAuth.state.CurrentUser
					if c.routineAuth.engine != s.Engine() || catalog == nil || c.authority.scope != nil {
						t.Fatal("DECLARE did not detach its original routine authority")
					}
					original := slices.Clone(c.routineDependencies)
					selected := -1
					for i, dependency := range original {
						if dependency.ref.Address.ClassID == ProcedureClassID {
							selected = i
							break
						}
					}
					if selected < 0 {
						t.Fatal("actual declaration lacks selected routine manifest")
					}
					c.routineDependencies = slices.Clone(original)
					switch mutation {
					case "relation_metadata":
						// Genuine same-transaction metadata publication changes a
						// base relation revision, not its captured input layout.
						sessionSQL(t, s, "ALTER TABLE unit_rows ENABLE ROW LEVEL SECURITY")
					case "missing":
						c.routineDependencies = append(c.routineDependencies[:selected], c.routineDependencies[selected+1:]...)
					case "foreign":
						c.routineDependencies[selected].ref = New().domain.Ref(c.routineDependencies[selected].ref.Epoch, c.routineDependencies[selected].ref.Address)
					case "epoch":
						c.routineDependencies[selected].ref.Epoch++
					case "revision":
						c.routineDependencies[selected].revision++
					}
					query := "FETCH 1 FROM c"
					if promotion {
						query = "COMMIT"
					}
					result, err := cursorUnitExecute(t, s, context.Background(), query)
					if mutation != "intact" && mutation != "relation_metadata" {
						if err == nil || result.Rows != nil || result.Count != 0 || c.source != nil || c.routineAuth.engine != nil {
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
						if !c.promoted || c.routineAuth.engine != nil || c.routineDependencies != nil || c.root != nil {
							t.Fatal("promotion retained declaration authority")
						}
					} else {
						if c.root != root || c.routineAuth.catalog.state != catalog || c.routineAuth.state.CurrentUser != actor || !reflect.DeepEqual(c.routineDependencies, original) || c.authority.scope != nil {
							t.Fatal("FETCH replaced original graph, authority or manifest")
						}
					}
					sessionSQL(t, s, "CLOSE c")
					if c.routineAuth.engine != nil || c.routineDependencies != nil {
						t.Fatal("CLOSE retained declaration authority")
					}
				})
			}
		})
	}
}
