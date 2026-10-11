package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsLifecycleProviders(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE SEQUENCE action_sequence START 10", 0))
		run(fixture.Exec("CREATE TABLE p(id BIGINT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(id INT,pid BIGINT DEFAULT nextval('action_sequence') CONSTRAINT fk REFERENCES p ON DELETE SET DEFAULT)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(2),(10),(11),(12)", 4))
		run(fixture.Exec("INSERT INTO c VALUES(1,2),(2,2)", 2))
		if !b.PG {
			foreignKeyRoundtrip(t, p)
		}
		run(fixture.Prepared(fixture.Exec("DELETE FROM p WHERE id=2", 1)))
		// Each demanded row consumes one value; physical visitation order is
		// not a row-ID assignment promise. Assert the exact bag instead.
		s := fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT8"}, [][]any{{int64(10)}, {int64(11)}})
		s.Bag = true
		run(s)
		run(fixture.Select("SELECT last_value FROM action_sequence", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(11)}}))
		if !b.PG {
			foreignKeyRoundtrip(t, p)
		}
		run(fixture.Exec("DELETE FROM p WHERE id=10", 1))
		run(fixture.Select("SELECT pid FROM c ORDER BY pid", []string{"pid"}, []string{"INT8"}, [][]any{{int64(11)}, {int64(12)}}))
		run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(11)}, {int64(12)}}))
		run(fixture.Select("SELECT last_value FROM action_sequence", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(12)}}))
	})
}
