package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func triggerDDLPartitionSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TABLE tr_parent(id integer,v integer) PARTITION BY RANGE(id)",
		"CREATE TABLE tr_mid PARTITION OF tr_parent FOR VALUES FROM (0) TO (100) PARTITION BY RANGE(id)",
		"CREATE TABLE tr_leaf PARTITION OF tr_mid FOR VALUES FROM (0) TO (50)",
		"CREATE TABLE tr_sibling PARTITION OF tr_mid FOR VALUES FROM (50) TO (100)",
		"CREATE FUNCTION tr_add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+TG_ARGV[0]::integer; RETURN NEW; END$$",
		"CREATE TRIGGER a BEFORE INSERT ON tr_parent FOR EACH ROW EXECUTE FUNCTION tr_add(3)")
}

func TestReferenceTriggerDDLPartition(t *testing.T) {
	t.Run("rename-local-extension", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerDDLPartitionSeed(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			userOperatorExec(t, p, b, c, "CREATE EXTENSION unaccent", "ALTER TRIGGER a ON tr_leaf DEPENDS ON EXTENSION unaccent")
			q := "INSERT INTO tr_parent VALUES($1,5) RETURNING id,v"
			held := triggerDDLHeld(t, p, b, c, q)
			leafQ := "INSERT INTO tr_leaf VALUES($1,5) RETURNING id,v"
			leaf := triggerDDLHeld(t, p, b, c, leafQ)
			run(fixture.Prepared(fixture.Exec(`ALTER TRIGGER a ON tr_parent RENAME TO "new name"`, 0)))
			run(triggerDDLX("tr_leaf", "unaccent", 1))
			run(triggerDDLX("tr_parent", "unaccent", 0))
			for _, id := range []int64{1, 51} {
				s := triggerRows(q, []any{id, int64(8)})
				s.Args = []any{id}
				held(s)
			}
			s := triggerRows(leafQ, []any{int64(2), int64(8)})
			s.Args = []any{2}
			leaf(s)
			userOperatorExec(t, p, b, c, "CREATE TABLE tr_future(id integer,v integer)", "ALTER TABLE tr_parent ATTACH PARTITION tr_future FOR VALUES FROM (100) TO (200)")
			run(triggerRows("INSERT INTO tr_future VALUES(101,5) RETURNING id,v", []any{int64(101), int64(8)}))
			run(fixture.Exec(`ALTER TRIGGER "new name" ON tr_leaf NO DEPENDS ON EXTENSION unaccent`, 0))
			run(fixture.Exec(`DROP TRIGGER "new name" ON tr_parent`, 0))
			s = triggerRows(q, []any{int64(3), int64(5)})
			s.Args = []any{3}
			held(s)
			s = triggerRows(leafQ, []any{int64(4), int64(5)})
			s.Args = []any{4}
			leaf(s)
			run(fixture.Select("SELECT count(*) AS n FROM tr_parent", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
			userOperatorExec(t, p, b, c, "DROP FUNCTION tr_add() RESTRICT", "DROP EXTENSION unaccent RESTRICT")
		})
	})
	t.Run("collision", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerDDLPartitionSeed(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE TRIGGER taken BEFORE INSERT ON tr_sibling FOR EACH ROW EXECUTE FUNCTION tr_add(7)")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(triggerDDLError("ALTER TRIGGER a ON tr_parent RENAME TO taken", "42710", `trigger "taken" for relation "tr_sibling" already exists`, true))
			run(triggerRows("INSERT INTO tr_parent VALUES(1,5) RETURNING id,v", []any{int64(1), int64(8)}))
			run(triggerRows("INSERT INTO tr_parent VALUES(51,5) RETURNING id,v", []any{int64(51), int64(15)}))
			run(fixture.Exec("DROP TRIGGER a ON tr_parent", 0))
			run(triggerRows("INSERT INTO tr_sibling VALUES(52,5) RETURNING id,v", []any{int64(52), int64(12)}))
		})
	})
	for _, action := range []string{"rename", "drop", "drop-cascade"} {
		t.Run("clone-"+action, func(t *testing.T) {
			triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				triggerDDLPartitionSeed(t, p, b, c)
				q := "DROP TRIGGER a ON tr_leaf"
				want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop trigger a on table tr_leaf because trigger a on table tr_mid requires it")
				want.Hint = "You can drop trigger a on table tr_mid instead."
				if action == "rename" {
					q = "ALTER TRIGGER a ON tr_leaf RENAME TO renamed"
					want = fixture.ControlsReferenceDiagnostic("0A000", `cannot rename trigger "a" on table "tr_leaf"`)
					want.Hint = `Rename the trigger on the partitioned table "tr_mid" instead.`
				}
				if action == "drop-cascade" {
					q += " CASCADE"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.ErrorDiagnostic(q, want)))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("INSERT INTO tr_leaf VALUES(1,5) RETURNING id,v", []any{int64(1), int64(8)}))
			})
		})
	}
}
