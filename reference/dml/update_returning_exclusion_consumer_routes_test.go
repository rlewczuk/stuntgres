package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningExcludedMechanismsConsumerRoutes(t *testing.T) {
	for _, query := range []string{"UPDATE t SET id=2 FROM t RETURNING id", "INSERT INTO t VALUES(2) RETURNING id", "CREATE TRIGGER change BEFORE UPDATE ON t FOR EACH ROW EXECUTE FUNCTION adjust_update()"} {
		t.Run(query, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				db := backend.DB
				fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
				fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1)")
				result, err := db.Exec(query)
				want := [][]any{{int64(1)}}
				if !backend.PG && query != "INSERT INTO t VALUES(2) RETURNING id" {
					if result != nil || err == nil {
						t.Fatal("excluded mechanism became native success", query, result, err)
					}
				} else {
					switch query {
					case "UPDATE t SET id=2 FROM t RETURNING id":
						if result != nil {
							t.Fatal("failed Exec exposed result", result)
						}
						fixture.State(t, err, "42712")
					case "CREATE TRIGGER change BEFORE UPDATE ON t FOR EACH ROW EXECUTE FUNCTION adjust_update()":
						if result != nil {
							t.Fatal("failed Exec exposed result", result)
						}
						fixture.State(t, err, "42883")
					case "INSERT INTO t VALUES(2) RETURNING id":
						fixture.DeleteReferenceCount(t, result, err, 1)
						want = [][]any{{int64(1)}, {int64(2)}}
					}
				}
				rows, err := db.Query("SELECT id FROM t")
				fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: want, Bag: true})
			}
		})
	}
}
