package reference_test

import "testing"

func TestReferencePGDependCoreEdgesUnownedSequence(t *testing.T) {
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE SEQUENCE pgdep_unowned", "CREATE TABLE pgdep_sequence_user(a BIGINT DEFAULT nextval('pgdep_unowned'))")
		table := pgDependRelation(t, b, "pgdep_sequence_user")
		seq := pgDependRelation(t, b, "pgdep_unowned")
		d := pgDependDefinition(t, b, table, 1)
		pgDependBag(t, p, b, 1259, seq, pgDependEdge(1259, seq, 0, 2615, pgDependNamespace(t, b, seq), 0, "n"))
		pgDependBag(t, p, b, 2604, d, pgDependEdge(2604, d, 0, 1259, table, 1, "a"), pgDependEdge(2604, d, 0, 1259, seq, 0, "n"))
		pgDependError(t, p, b, "DROP SEQUENCE pgdep_unowned", pgDependRestrict("sequence pgdep_unowned", "default value for column a of table pgdep_sequence_user depends on sequence pgdep_unowned"))
		pgDependExec(t, p, b, "ALTER SEQUENCE pgdep_unowned RENAME TO pgdep_sequence_renamed")
		if pgDependRelation(t, b, "pgdep_sequence_renamed") != seq {
			t.Fatal("sequence rename reminted identity")
		}
		pgDependBag(t, p, b, 2604, d, pgDependEdge(2604, d, 0, 1259, table, 1, "a"), pgDependEdge(2604, d, 0, 1259, seq, 0, "n"))
		pgDependExec(t, p, b, "DROP TABLE pgdep_sequence_user", "DROP SEQUENCE pgdep_sequence_renamed")
		pgDependBag(t, p, b, 2604, d)
		pgDependBag(t, p, b, 1259, seq)
	})
}
