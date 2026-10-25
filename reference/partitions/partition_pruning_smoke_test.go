package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionPruningFeatureSmoke(t *testing.T) {
	referencePruningScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		// One shared control witness supplies mixed RANGE/LIST/HASH ancestry,
		// duplicate1/boundary3/default5/NULL, inheritance UNKNOWN/NO INHERIT,
		// same-held rebinding, empty metadata, native replacement and SQL replay.
		referencePruningControlBody(t, p, b, prepared)
		run := partitionConstraintRun(t, p, b, prepared)
		referenceListSetup(run)
		run(fixture.Exec("INSERT INTO lp VALUES(1,'a'),(NULL,'n'),(7,'d')", 3))
		referencePruningKeys(run, b.PG, "SELECT k FROM lp WHERE k IS NULL", nil)
		referencePruningKeys(run, b.PG, "SELECT k FROM lp WHERE k=7", int64(7))
		referencePruningKeys(run, b.PG, "SELECT k FROM lp WHERE k<>1", int64(7))
		run(fixture.Exec("CREATE TABLE pp_smoke_hash(k INT) PARTITION BY HASH(k)", 0))
		referenceHashChildren(run, "pp_smoke_hash", 4)
		run(fixture.Exec("INSERT INTO pp_smoke_hash VALUES(1),(1),(2),(NULL)", 4))
		referencePruningKeys(run, b.PG, "SELECT k FROM pp_smoke_hash WHERE k=1", int64(1), int64(1))
		referencePruningKeys(run, b.PG, "SELECT k FROM pp_smoke_hash WHERE k IS NULL", nil)
	})
}
