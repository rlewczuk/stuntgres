package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningExcludedMechanisms(t *testing.T) {
	for _, query := range []string{"UPDATE t SET id=2 RETURNING old", "UPDATE t SET id=2 RETURNING (old).*", "UPDATE t SET id=2 FROM t RETURNING id", "INSERT INTO t VALUES(2) RETURNING id", "CREATE TRIGGER change BEFORE UPDATE ON t FOR EACH ROW EXECUTE FUNCTION adjust_update()"} {
		t.Run(query, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				want := [][]any{{int64(1)}}
				if backend.PG {
					switch query {
					case "UPDATE t SET id=2 FROM t RETURNING id":
						run(fixture.Error(query, "42712"))
					case "CREATE TRIGGER change BEFORE UPDATE ON t FOR EACH ROW EXECUTE FUNCTION adjust_update()":
						run(fixture.Error(query, "42883"))
					case "INSERT INTO t VALUES(2) RETURNING id":
						run(fixture.Exec(query, 1))
						want = [][]any{{int64(1)}, {int64(2)}}
					default:
						run(fixture.Exec(query, 1))
						want = [][]any{{int64(2)}}
					}
				} else {
					result, err := backend.DB.Exec(query)
					if err == nil || result != nil {
						t.Fatal("excluded mechanism became native success", query, result, err)
					}
				}
				run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, want))
			}
		})
	}
}
