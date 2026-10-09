package reference_test

import "testing"

func TestReferenceUpdateReturningExcludedMechanisms(t *testing.T) {
	for _, query := range []string{"UPDATE t SET id=2 RETURNING (SELECT 7)", "UPDATE t SET id=2 RETURNING old", "UPDATE t SET id=2 RETURNING (old).*", "UPDATE t SET id=2 FROM t RETURNING id", "INSERT INTO t VALUES(2) RETURNING id", "CREATE TRIGGER change BEFORE UPDATE ON t FOR EACH ROW EXECUTE FUNCTION adjust_update()"} {
		t.Run(query, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1)", 1))
				want := [][]any{{int64(1)}}
				if backend.pg {
					switch query {
					case "UPDATE t SET id=2 FROM t RETURNING id":
						run(referenceError(query, "42712"))
					case "CREATE TRIGGER change BEFORE UPDATE ON t FOR EACH ROW EXECUTE FUNCTION adjust_update()":
						run(referenceError(query, "42883"))
					case "INSERT INTO t VALUES(2) RETURNING id":
						run(referenceExec(query, 1))
						want = [][]any{{int64(1)}, {int64(2)}}
					default:
						run(referenceExec(query, 1))
						want = [][]any{{int64(2)}}
					}
				} else {
					result, err := backend.db.Exec(query)
					if err == nil || result != nil {
						t.Fatal("excluded mechanism became native success", query, result, err)
					}
				}
				run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, want))
			}
		})
	}
}
