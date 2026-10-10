package reference_test

import "testing"

func TestReferencePGDependFeatureSmoke(t *testing.T) {
	// Reuse complete shared scenario bodies, including native-only Dump/replay,
	// rather than maintain a narrower alternate smoke/oracle implementation.
	t.Run("construction", TestReferencePGDependCoreEdges)
	t.Run("domain_array", TestReferencePGDependTypeEdges)
	t.Run("stable_attributes", TestReferencePGDependRenameStableAttributes)
	t.Run("retained_and_prepared", TestReferencePGDependSnapshots)
	t.Run("restrict_and_cascade", func(t *testing.T) {
		p := newReferenceScenario(t)
		pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
			pgDependExec(t, p, b, "CREATE TABLE pgdep_smoke_p(a INT)", "CREATE TABLE pgdep_smoke_t(c BOOL DEFAULT ('pgdep_smoke_p'::regclass IS NOT NULL))")
			provider := pgDependRelation(t, b, "pgdep_smoke_p")
			table := pgDependRelation(t, b, "pgdep_smoke_t")
			d := pgDependDefinition(t, b, table, 1)
			pgDependBag(t, p, b, 2604, d, pgDependEdge(2604, d, 0, 1259, table, 1, "a"), pgDependEdge(2604, d, 0, 1259, provider, 0, "n"))
			pgDependError(t, p, b, "DROP TABLE pgdep_smoke_p", pgDependRestrict("table pgdep_smoke_p", "default value for column c of table pgdep_smoke_t depends on table pgdep_smoke_p"))
			pgDependExec(t, p, b, "DROP TABLE pgdep_smoke_p CASCADE")
			pgDependBag(t, p, b, 2604, d)
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT c FROM pgdep_smoke_t", []string{"c"}, []string{"BOOL"}, [][]any{}))
		})
	})
	t.Run("controls_and_actual_replay", TestReferencePGDependControls)
}
