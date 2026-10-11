package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDependCoreEdgesUnownedSequence(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE SEQUENCE pgdep_unowned", "CREATE TABLE pgdep_sequence_user(a BIGINT DEFAULT nextval('pgdep_unowned'))")
		table := fixture.PgDependRelation(t, b, "pgdep_sequence_user")
		seq := fixture.PgDependRelation(t, b, "pgdep_unowned")
		d := fixture.PgDependDefinition(t, b, table, 1)
		fixture.PgDependBag(t, p, b, 1259, seq, fixture.PgDependEdge(1259, seq, 0, 2615, fixture.PgDependNamespace(t, b, seq), 0, "n"))
		fixture.PgDependBag(t, p, b, 2604, d, fixture.PgDependEdge(2604, d, 0, 1259, table, 1, "a"), fixture.PgDependEdge(2604, d, 0, 1259, seq, 0, "n"))
		fixture.PgDependError(t, p, b, "DROP SEQUENCE pgdep_unowned", fixture.PgDependRestrict("sequence pgdep_unowned", "default value for column a of table pgdep_sequence_user depends on sequence pgdep_unowned"))
		fixture.PgDependExec(t, p, b, "ALTER SEQUENCE pgdep_unowned RENAME TO pgdep_sequence_renamed")
		if fixture.PgDependRelation(t, b, "pgdep_sequence_renamed") != seq {
			t.Fatal("sequence rename reminted identity")
		}
		fixture.PgDependBag(t, p, b, 2604, d, fixture.PgDependEdge(2604, d, 0, 1259, table, 1, "a"), fixture.PgDependEdge(2604, d, 0, 1259, seq, 0, "n"))
		fixture.PgDependExec(t, p, b, "DROP TABLE pgdep_sequence_user", "DROP SEQUENCE pgdep_sequence_renamed")
		fixture.PgDependBag(t, p, b, 2604, d)
		fixture.PgDependBag(t, p, b, 1259, seq)
	})
}
