package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func insteadFlags(q string, rows ...[]any) fixture.Step {
	return fixture.Select(q, []string{"table_name", "is_updatable", "is_insertable_into", "is_trigger_updatable", "is_trigger_deletable", "is_trigger_insertable_into"}, []string{"NAME", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"}, rows)
}
func TestReferenceTriggerInsteadMetadata(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		insteadSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE VIEW io_nonauto AS SELECT id,v FROM io_base UNION ALL SELECT id,v FROM io_base", "CREATE VIEW io_outer AS SELECT id,v FROM io_view")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		flags := "SELECT table_name,is_updatable,is_insertable_into,is_trigger_updatable,is_trigger_deletable,is_trigger_insertable_into FROM information_schema.views WHERE table_schema='public' AND table_name IN ('io_nonauto','io_outer','io_view') ORDER BY table_name"
		run(insteadFlags(flags, []any{"io_nonauto", "NO", "NO", "NO", "NO", "NO"}, []any{"io_outer", "YES", "YES", "NO", "NO", "NO"}, []any{"io_view", "YES", "YES", "YES", "YES", "YES"}))
		userOperatorExec(t, p, b, c, "CREATE TRIGGER z INSTEAD OF INSERT OR UPDATE ON io_nonauto FOR EACH ROW EXECUTE FUNCTION io_body('x''y')", "CREATE TRIGGER a INSTEAD OF INSERT OR UPDATE ON io_nonauto FOR EACH ROW EXECUTE FUNCTION io_body('first')")
		run(insteadFlags(flags, []any{"io_nonauto", "NO", "NO", "YES", "NO", "YES"}, []any{"io_outer", "YES", "YES", "NO", "NO", "NO"}, []any{"io_view", "YES", "YES", "YES", "YES", "YES"}))
		run(fixture.Select("SELECT relhastriggers FROM pg_class WHERE oid='io_nonauto'::regclass", []string{"relhastriggers"}, []string{"BOOL"}, [][]any{{true}}))
		q := "SELECT trigger_name,event_manipulation,action_timing,action_orientation,action_condition,action_order FROM information_schema.triggers WHERE event_object_schema='public' AND event_object_table='io_nonauto' ORDER BY event_manipulation,action_order"
		run(fixture.Select(q, []string{"trigger_name", "event_manipulation", "action_timing", "action_orientation", "action_condition", "action_order"}, []string{"NAME", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "INT4"}, [][]any{{"a", "INSERT", "INSTEAD OF", "ROW", nil, int64(1)}, {"z", "INSERT", "INSTEAD OF", "ROW", nil, int64(2)}, {"a", "UPDATE", "INSTEAD OF", "ROW", nil, int64(1)}, {"z", "UPDATE", "INSTEAD OF", "ROW", nil, int64(2)}}))
		s := fixture.Select("INSERT INTO io_view VALUES(1,10) RETURNING id AS key,v AS transformed", []string{"key", "transformed"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(110)}})
		run(s)
		run(fixture.Select("UPDATE io_view SET v=0 WHERE false RETURNING id AS key,v AS transformed", []string{"key", "transformed"}, []string{"INT4", "INT4"}, nil))
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION io_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=777; RETURN NEW; END$$")
		s.Rows = [][]any{{int64(2), int64(777)}}
		s.Query = "INSERT INTO io_view VALUES(2,20) RETURNING id AS key,v AS transformed"
		run(s)
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER a INSTEAD OF DELETE ON io_nonauto FOR EACH ROW EXECUTE FUNCTION io_body()", "DROP TRIGGER z ON io_nonauto")
		run(insteadFlags(flags, []any{"io_nonauto", "NO", "NO", "NO", "YES", "NO"}, []any{"io_outer", "YES", "YES", "NO", "NO", "NO"}, []any{"io_view", "YES", "YES", "YES", "YES", "YES"}))
		userOperatorExec(t, p, b, c, "DROP TRIGGER a ON io_nonauto")
		run(insteadFlags(flags, []any{"io_nonauto", "NO", "NO", "NO", "NO", "NO"}, []any{"io_outer", "YES", "YES", "NO", "NO", "NO"}, []any{"io_view", "YES", "YES", "YES", "YES", "YES"}))
	})
}
