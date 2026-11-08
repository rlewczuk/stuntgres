package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDDLNameTruncationTransactions(t *testing.T) {
	triggerNameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		triggerNameSetup(run)
		defer c.ExecContext(context.Background(), "ROLLBACK")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec(triggerNameCreate("tn_data", triggerLongSource, "3"), 0))
		run(triggerNameRows("INSERT INTO tn_data VALUES(1,5,NULL) RETURNING id,v,mark", []any{int64(1), int64(8), triggerLongCanonical}))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT count(*) FROM tn_data", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_trigger WHERE tgrelid='tn_data'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec(triggerNameCreate("tn_data", triggerLongSource, "3"), 0))
		run(fixture.Exec("COMMIT", 0))
		oid := triggerNameOID(t, p, c, "tn_data", triggerLongCanonical)
		observer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		observe := func() {
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT tgname FROM pg_trigger WHERE tgrelid='tn_data'::regclass", []string{"tgname"}, []string{"NAME"}, [][]any{{triggerLongCanonical}}))
		}
		for i, q := range []string{
			"CREATE OR REPLACE TRIGGER " + triggerLongSource + " BEFORE INSERT OR UPDATE OF v ON tn_data FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION tn_add('7')",
			"ALTER TRIGGER " + triggerLongCanonical + " ON tn_data RENAME TO temporary",
			"DROP TRIGGER " + triggerLongCanonical + " ON tn_data",
		} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT before_ddl", 0))
			run(fixture.Exec(q, 0))
			observe()
			value := int64(12)
			var mark any = triggerLongCanonical
			if i == 1 {
				value = 8
				mark = "temporary"
			}
			if i == 2 {
				value = 5
				mark = nil
			}
			run(triggerNameRows("INSERT INTO tn_data VALUES(20,5,NULL) RETURNING id,v,mark", []any{int64(20), value, mark}))
			run(fixture.Exec("ROLLBACK TO before_ddl", 0))
			triggerNameCatalog(run, "tn_data", triggerLongCanonical)
			if triggerNameOID(t, p, c, "tn_data", triggerLongCanonical) != oid {
				t.Fatal("savepoint changed OID")
			}
			run(fixture.Select("SELECT count(*) FROM tn_data", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(triggerNameRows("INSERT INTO tn_data VALUES(21,5,NULL) RETURNING id,v,mark", []any{int64(21), int64(8), triggerLongCanonical}))
			run(fixture.Exec("ROLLBACK", 0))
			observe()
		}
		run(fixture.Exec(triggerNameCreate("tn_data", "occupied", "7"), 0))
		for _, q := range []string{triggerNameCreate("tn_data", triggerLongSource, "9"), "ALTER TRIGGER occupied ON tn_data RENAME TO " + triggerLongSource} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT before_error", 0))
			run(triggerNameError(q, "42710", `trigger "`+triggerLongCanonical+`" for relation "tn_data" already exists`))
			run(triggerNameError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
			run(fixture.Exec("ROLLBACK TO before_error", 0))
			if triggerNameOID(t, p, c, "tn_data", triggerLongCanonical) != oid {
				t.Fatal("failed DDL changed OID")
			}
			run(triggerNameRows("INSERT INTO tn_data VALUES(1,5,NULL) RETURNING id,v,mark", []any{int64(1), int64(15), triggerLongCanonical}))
			run(fixture.Exec("ROLLBACK", 0))
		}
		run(fixture.Exec("DROP TRIGGER occupied ON tn_data", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER TRIGGER "+triggerLongCanonical+" ON tn_data RENAME TO committed", 0))
		observe()
		run(fixture.Exec("COMMIT", 0))
		triggerNameCatalog(run, "tn_data", "committed")
		if triggerNameOID(t, p, c, "tn_data", "committed") != oid {
			t.Fatal("committed rename changed OID")
		}
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT tgname FROM pg_trigger WHERE tgrelid='tn_data'::regclass", []string{"tgname"}, []string{"NAME"}, [][]any{{"committed"}}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE OR REPLACE TRIGGER committed BEFORE INSERT ON tn_data FOR EACH ROW EXECUTE FUNCTION tn_add('7')", 0))
		run(fixture.Exec("COMMIT", 0))
		if triggerNameOID(t, p, c, "tn_data", "committed") != oid {
			t.Fatal("committed replace changed OID")
		}
		run(triggerNameRows("INSERT INTO tn_data VALUES(2,5,NULL) RETURNING id,v,mark", []any{int64(2), int64(12), "committed"}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DROP TRIGGER committed ON tn_data", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(triggerNameRows("INSERT INTO tn_data VALUES(3,5,NULL) RETURNING id,v,mark", []any{int64(3), int64(12), "committed"}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DROP TRIGGER committed ON tn_data", 0))
		run(fixture.Exec("COMMIT", 0))
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_trigger WHERE tgrelid='tn_data'::regclass AND NOT tgisinternal", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(triggerNameRows("INSERT INTO tn_data VALUES(4,5,NULL) RETURNING id,v,mark", []any{int64(4), int64(5), nil}))
	})
}

const triggerMigration278Function = `CREATE FUNCTION nullify_next_start_at_on_workspace_autostart_modification() RETURNS trigger
	LANGUAGE plpgsql
AS $$
DECLARE
BEGIN
	-- A workspace's next_start_at might be invalidated by the following:
	--   * The autostart schedule has changed independent to next_start_at
	--   * The workspace has been marked as dormant
	IF (NEW.autostart_schedule <> OLD.autostart_schedule AND NEW.next_start_at = OLD.next_start_at)
		OR (NEW.dormant_at IS NOT NULL AND NEW.next_start_at IS NOT NULL)
	THEN
		UPDATE workspaces
		SET next_start_at = NULL
		WHERE id = NEW.id;
	END IF;
	RETURN NEW;
END;
$$;`

const triggerMigration278Declaration = `CREATE TRIGGER trigger_nullify_next_start_at_on_workspace_autostart_modification
	AFTER UPDATE ON workspaces
	FOR EACH ROW
EXECUTE PROCEDURE nullify_next_start_at_on_workspace_autostart_modification();`

func triggerNameMigration278(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE workspaces(id uuid PRIMARY KEY,autostart_schedule text,next_start_at timestamptz,dormant_at timestamptz)", 0))
	run(fixture.Exec(triggerMigration278Function, 0))
	run(fixture.Exec(triggerMigration278Declaration, 0))
	triggerNameCatalog(run, "workspaces", triggerLongCanonical)
	triggerNameOID(t, p, c, "workspaces", triggerLongCanonical)
	run(fixture.Exec("INSERT INTO workspaces VALUES('00000000-0000-0000-0000-000000000001','old','2030-01-01 UTC',NULL),('00000000-0000-0000-0000-000000000002','old','2030-01-01 UTC',NULL),('00000000-0000-0000-0000-000000000003','old','2030-01-01 UTC',NULL),('00000000-0000-0000-0000-000000000004','old','2030-01-01 UTC',NULL)", 4))
	run(fixture.Exec("UPDATE workspaces SET autostart_schedule='changed' WHERE id='00000000-0000-0000-0000-000000000001'", 1))
	run(fixture.Exec("UPDATE workspaces SET dormant_at='2029-01-01 UTC' WHERE id='00000000-0000-0000-0000-000000000002'", 1))
	run(fixture.Exec("UPDATE workspaces SET autostart_schedule='changed',next_start_at='2031-01-01 UTC' WHERE id='00000000-0000-0000-0000-000000000003'", 1))
	run(fixture.Exec("UPDATE workspaces SET autostart_schedule=autostart_schedule WHERE id='00000000-0000-0000-0000-000000000004'", 1))
	run(fixture.Select("SELECT id::text,autostart_schedule,next_start_at IS NULL AS cleared,next_start_at IS NOT DISTINCT FROM '2031-01-01 UTC'::timestamptz AS rescheduled,next_start_at IS NOT DISTINCT FROM '2030-01-01 UTC'::timestamptz AS preserved FROM workspaces ORDER BY id", []string{"id", "autostart_schedule", "cleared", "rescheduled", "preserved"}, []string{"TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{
		{"00000000-0000-0000-0000-000000000001", "changed", true, false, false},
		{"00000000-0000-0000-0000-000000000002", "old", true, false, false},
		{"00000000-0000-0000-0000-000000000003", "changed", false, true, false},
		{"00000000-0000-0000-0000-000000000004", "old", false, false, true},
	}))
}

func TestReferenceTriggerDDLNameTruncationMigration278(t *testing.T) {
	triggerNameScenario(t, triggerNameMigration278)
}
