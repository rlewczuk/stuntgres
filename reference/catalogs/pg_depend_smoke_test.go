package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDependFeatureSmoke(t *testing.T) {
	// Reuse complete shared scenario bodies, including native-only Dump/replay,
	// rather than maintain a narrower alternate smoke/oracle implementation.
	t.Run("construction", TestReferencePGDependCoreEdges)
	t.Run("domain_array", TestReferencePGDependTypeEdges)
	t.Run("stable_attributes", TestReferencePGDependRenameStableAttributes)
	t.Run("retained_and_prepared", TestReferencePGDependSnapshots)
	t.Run("restrict_and_cascade", func(t *testing.T) {
		p := fixture.NewScenario(t)
		fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
			fixture.PgDependExec(t, p, b, "CREATE TABLE pgdep_smoke_p(a INT)", "CREATE TABLE pgdep_smoke_t(c BOOL DEFAULT ('pgdep_smoke_p'::regclass IS NOT NULL))")
			provider := fixture.PgDependRelation(t, b, "pgdep_smoke_p")
			table := fixture.PgDependRelation(t, b, "pgdep_smoke_t")
			d := fixture.PgDependDefinition(t, b, table, 1)
			fixture.PgDependBag(t, p, b, 2604, d, fixture.PgDependEdge(2604, d, 0, 1259, table, 1, "a"), fixture.PgDependEdge(2604, d, 0, 1259, provider, 0, "n"))
			fixture.PgDependError(t, p, b, "DROP TABLE pgdep_smoke_p", fixture.PgDependRestrict("table pgdep_smoke_p", "default value for column c of table pgdep_smoke_t depends on table pgdep_smoke_p"))
			fixture.PgDependExec(t, p, b, "DROP TABLE pgdep_smoke_p CASCADE")
			fixture.PgDependBag(t, p, b, 2604, d)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT c FROM pgdep_smoke_t", []string{"c"}, []string{"BOOL"}, [][]any{}))
		})
	})
	t.Run("controls_and_actual_replay", TestReferencePGDependControls)
}
