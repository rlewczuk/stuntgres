package reference_test

import (
	"testing"
)

func TestReferenceUpdateReturningExcludedMechanismsConsumerRoutes(t *testing.T) {
	for _, query := range []string{"UPDATE t SET id=2 RETURNING (SELECT 7)", "UPDATE t SET id=2 RETURNING old", "UPDATE t SET id=2 RETURNING (old).*", "UPDATE t SET id=2 FROM t RETURNING id", "INSERT INTO t VALUES(2) RETURNING id", "CREATE TRIGGER change BEFORE UPDATE ON t FOR EACH ROW EXECUTE FUNCTION adjust_update()"} {
		t.Run(query, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				db := backend.db
				updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
				updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1)")
				result, err := db.Exec(query)
				want := [][]any{{int64(1)}}
				if !backend.pg {
					if result != nil || err == nil {
						t.Fatal("excluded mechanism became native success", query, result, err)
					}
				} else {
					switch query {
					case "UPDATE t SET id=2 FROM t RETURNING id":
						if result != nil {
							t.Fatal("failed Exec exposed result", result)
						}
						referenceState(t, err, "42712")
					case "CREATE TRIGGER change BEFORE UPDATE ON t FOR EACH ROW EXECUTE FUNCTION adjust_update()":
						if result != nil {
							t.Fatal("failed Exec exposed result", result)
						}
						referenceState(t, err, "42883")
					case "INSERT INTO t VALUES(2) RETURNING id":
						deleteReferenceCount(t, result, err, 1)
						want = [][]any{{int64(1)}, {int64(2)}}
					default:
						deleteReferenceCount(t, result, err, 1)
						want = [][]any{{int64(2)}}
					}
				}
				rows, err := db.Query("SELECT id FROM t")
				deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id"}, types: []string{"INT4"}, rows: want, bag: true})
			}
		})
	}
}
