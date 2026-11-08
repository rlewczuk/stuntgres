package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDDLNameTruncationWire(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		route := "direct"
		if prepared {
			route = "prepared"
		}
		t.Run(route, func(t *testing.T) {
			f := fixture.NewServerFixture(t)
			for _, b := range f.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, records := triggerWireConnection(t, f, b)
					runner := func(t *testing.T) func(fixture.Step) {
						return func(s fixture.Step) {
							*records = nil
							if prepared {
								s = fixture.Prepared(s)
								if s.Query == "SELECT 1" {
									s.ErrorStage = "prepare"
								}
							}
							fixture.RunStep(t, f.Ctx, c, b.Recorder, s)
							triggerWireNotices(t, b.PG, s.Query, *records)
						}
					}
					t.Run("minimal", func(t *testing.T) {
						defer c.ExecContext(context.Background(), "ROLLBACK")
						run := runner(t)
						// Preserve the saved five-statement wire reproducer verbatim.
						for _, q := range []string{
							"BEGIN;",
							"CREATE TABLE r3_long_trigger(id integer);",
							"CREATE FUNCTION r3_long_trigger_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END;$$;",
							"CREATE TRIGGER trigger_nullify_next_start_at_on_workspace_autostart_modification AFTER UPDATE ON r3_long_trigger FOR EACH ROW EXECUTE FUNCTION r3_long_trigger_fn();",
							"ROLLBACK;",
						} {
							run(fixture.Exec(q, 0))
						}
						run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='r3_long_trigger'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						run(fixture.Select("SELECT count(*) FROM pg_proc WHERE proname='r3_long_trigger_fn'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						run(fixture.Select("SELECT count(*) FROM pg_trigger WHERE tgname='"+triggerLongCanonical+"'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					})
					t.Run("committed", func(t *testing.T) {
						defer c.ExecContext(context.Background(), "ROLLBACK")
						run := runner(t)
						run(fixture.Exec("BEGIN", 0))
						run(fixture.Exec("CREATE TABLE wire_long_trigger(id integer PRIMARY KEY,v integer,mark text)", 0))
						run(fixture.Exec("CREATE FUNCTION wire_long_trigger_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+3; NEW.mark:=TG_NAME; RETURN NEW; END$$", 0))
						run(fixture.Exec("CREATE TRIGGER "+triggerLongSource+" BEFORE INSERT ON wire_long_trigger FOR EACH ROW EXECUTE FUNCTION wire_long_trigger_fn()", 0))
						run(fixture.Exec("COMMIT", 0))
						triggerNameCatalog(run, "wire_long_trigger", triggerLongCanonical)
						run(fixture.Select("SELECT oid>0 AS positive_oid FROM pg_trigger WHERE tgrelid='wire_long_trigger'::regclass AND tgname='"+triggerLongCanonical+"'", []string{"positive_oid"}, []string{"BOOL"}, [][]any{{true}}))
						run(fixture.Exec("INSERT INTO wire_long_trigger VALUES(1,5,NULL)", 1))
						run(triggerNameRows("SELECT id,v,mark FROM wire_long_trigger ORDER BY id", []any{int64(1), int64(8), triggerLongCanonical}))
						run(fixture.Exec("DROP TABLE wire_long_trigger", 0))
						run(fixture.Exec("DROP FUNCTION wire_long_trigger_fn()", 0))
					})
					t.Run("lifecycle", func(t *testing.T) {
						defer c.ExecContext(context.Background(), "ROLLBACK")
						triggerWireLifecycle(t, f, b, c, runner(t))
					})
					t.Run("partition", func(t *testing.T) {
						defer c.ExecContext(context.Background(), "ROLLBACK")
						triggerWirePartition(t, f, c, runner(t))
					})
					t.Run("migration278", func(t *testing.T) {
						defer c.ExecContext(context.Background(), "ROLLBACK")
						run := runner(t)
						run(fixture.Exec("BEGIN", 0))
						run(fixture.Exec("CREATE TABLE workspaces(id uuid PRIMARY KEY,autostart_schedule text,next_start_at timestamptz,dormant_at timestamptz)", 0))
						run(fixture.Exec(triggerMigration278Function, 0))
						run(fixture.Exec(triggerMigration278Declaration, 0))
						triggerNameCatalog(run, "workspaces", triggerLongCanonical)
						run(fixture.Exec("INSERT INTO workspaces VALUES('00000000-0000-0000-0000-000000000001','old','2030-01-01 UTC',NULL),('00000000-0000-0000-0000-000000000002','old','2030-01-01 UTC',NULL),('00000000-0000-0000-0000-000000000003','old','2030-01-01 UTC',NULL)", 3))
						run(fixture.Exec("UPDATE workspaces SET autostart_schedule='changed' WHERE id='00000000-0000-0000-0000-000000000001'", 1))
						run(fixture.Exec("UPDATE workspaces SET dormant_at='2029-01-01 UTC' WHERE id='00000000-0000-0000-0000-000000000002'", 1))
						run(fixture.Exec("UPDATE workspaces SET autostart_schedule=autostart_schedule WHERE id='00000000-0000-0000-0000-000000000003'", 1))
						run(fixture.Select("SELECT id::text,next_start_at IS NULL AS cleared FROM workspaces ORDER BY id", []string{"id", "cleared"}, []string{"TEXT", "BOOL"}, [][]any{
							{"00000000-0000-0000-0000-000000000001", true},
							{"00000000-0000-0000-0000-000000000002", true},
							{"00000000-0000-0000-0000-000000000003", false},
						}))
						run(fixture.Exec("ROLLBACK", 0))
					})
				})
			}
		})
	}
}
