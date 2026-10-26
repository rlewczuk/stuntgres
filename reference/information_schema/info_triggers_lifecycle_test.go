package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoTriggersLifecycle(t *testing.T) {
	itrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		itSeed(run)
		where := "trigger_schema IN ('it_scope','it_moved')"
		held := itHeld(t, p, c, r, itProjection(b.PG, where, nil).Query)
		check := func(want [][]any) { s := itProjection(b.PG, where, want); run(s); held(s) }
		all := itSeedRows(database, true)
		check(all)
		run(fixture.Exec("ALTER TRIGGER z ON it_scope.it_data RENAME TO b", 0))
		all[2][2], all[3][2] = "b", "b"
		all[3][7], all[1][7] = int64(1), int64(2)
		check(all)
		run(itProjection(b.PG, "trigger_schema='it_scope' AND event_object_table='it_data' AND trigger_name='m'", [][]any{all[1]}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER TRIGGER b ON it_scope.it_data RENAME TO c", 0))
		run(fixture.Exec("DROP TRIGGER m ON it_scope.it_data", 0))
		transactional := [][]any{all[0], itrRow(database, "it_scope", "it_data", "c", "INSERT", 2, "(new.v > 0)", "EXECUTE FUNCTION it_identity()"), itrRow(database, "it_scope", "it_data", "c", "UPDATE", 1, "(new.v > 0)", "EXECUTE FUNCTION it_identity()"), all[4]}
		check(transactional)
		run(fixture.Exec("ROLLBACK", 0))
		check(all)
		run(fixture.Exec("CREATE OR REPLACE TRIGGER b BEFORE UPDATE OR DELETE ON it_scope.it_data FOR EACH ROW EXECUTE FUNCTION public.it_identity()", 0))
		all = [][]any{all[0], all[1], itrRow(database, "it_scope", "it_data", "b", "UPDATE", 1, nil, "EXECUTE FUNCTION it_identity()"), itrRow(database, "it_scope", "it_data", "b", "DELETE", 1, nil, "EXECUTE FUNCTION it_identity()"), all[4]}
		check(all)
		run(fixture.Exec("ALTER TABLE it_scope.it_data RENAME COLUMN v TO value", 0))
		run(fixture.Exec("ALTER TABLE it_scope.it_data RENAME TO renamed", 0))
		run(fixture.Exec("CREATE SCHEMA it_moved", 0))
		run(fixture.Exec("ALTER TABLE it_scope.renamed SET SCHEMA it_moved", 0))
		for _, row := range all[:4] {
			row[1], row[5], row[6] = "it_moved", "it_moved", "renamed"
		}
		check(all)
		run(fixture.Exec("DROP TRIGGER b ON it_moved.renamed", 0))
		all = [][]any{all[0], all[1], all[4]}
		all[1][7] = int64(1)
		check(all)
		run(fixture.Exec("DROP TRIGGER a ON it_moved.renamed", 0))
		run(fixture.Exec("CREATE TRIGGER a BEFORE DELETE ON it_moved.renamed FOR EACH ROW EXECUTE FUNCTION public.it_identity()", 0))
		all[0] = itrRow(database, "it_moved", "renamed", "a", "DELETE", 1, nil, "EXECUTE FUNCTION it_identity()")
		check(all)
		// UPDATE OF follows the renamed attribute, without inventing companion rows.
		run(fixture.Exec("INSERT INTO it_moved.renamed VALUES(1,2,3)", 1))
		run(fixture.Exec("UPDATE it_moved.renamed SET value=4", 1))
		run(fixture.Exec("DROP TRIGGER m ON it_moved.renamed", 0))
		all = [][]any{all[0], all[2]}
		check(all)
		run(fixture.Exec("DROP TABLE it_moved.renamed", 0))
		check(all[1:])
		run(fixture.Exec("DROP TABLE it_scope.it_second", 0))
		check(nil)
	})
}
