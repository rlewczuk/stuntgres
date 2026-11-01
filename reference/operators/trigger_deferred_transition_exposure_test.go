package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDeferredTransitionExposure(t *testing.T) {
	for _, boundary := range []string{"commit", "set", "savepoint", "immediate"} {
		t.Run(boundary, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				userOperatorExec(t, p, b, c,
					"CREATE TABLE dt_parent(id integer PRIMARY KEY)",
					"CREATE TABLE dt_child(id integer,pid integer REFERENCES dt_parent ON DELETE CASCADE)",
					"CREATE TABLE x(id integer)",
					"CREATE TABLE dt_audit(seq integer GENERATED ALWAYS AS IDENTITY,label text,id integer,image bigint)",
					"CREATE TABLE dt_when(id integer)",
					"CREATE FUNCTION dt_accept(i integer) RETURNS boolean LANGUAGE plpgsql AS $$BEGIN INSERT INTO dt_when VALUES(i); RETURN true; END$$",
					"CREATE FUNCTION dt_scalar() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO dt_audit(label,id,image) VALUES(TG_NAME,OLD.id,(SELECT sum(id) FROM x)); RETURN NULL; END$$",
					"CREATE FUNCTION dt_transition() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO dt_audit(label,id,image) VALUES(TG_NAME,OLD.id,(SELECT sum(id) FROM x)); RETURN NULL; END$$",
					"CREATE CONSTRAINT TRIGGER a_deferred AFTER DELETE ON dt_child DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(dt_accept(OLD.id)) EXECUTE FUNCTION dt_scalar()",
					"CREATE CONSTRAINT TRIGGER b_immediate AFTER DELETE ON dt_child NOT DEFERRABLE FOR EACH ROW EXECUTE FUNCTION dt_scalar()",
					"CREATE TRIGGER m_transition AFTER DELETE ON dt_child REFERENCING OLD TABLE x FOR EACH ROW EXECUTE FUNCTION dt_transition()",
					"CREATE CONSTRAINT TRIGGER z_deferred AFTER DELETE ON dt_child DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(dt_accept(OLD.id)) EXECUTE FUNCTION dt_scalar()")
				run := func(step fixture.Step) { afterStep(t, p, b, c, prepared, step) }
				run(fixture.Exec("INSERT INTO x VALUES(900)", 1))
				run(fixture.Exec("INSERT INTO dt_parent VALUES(1)", 1))
				run(fixture.Exec("INSERT INTO dt_child VALUES(11,1)", 1))
				audit := func(rows ...[]any) fixture.Step {
					return fixture.Select("SELECT label,id,image FROM dt_audit ORDER BY seq", []string{"label", "id", "image"}, []string{"TEXT", "INT4", "INT8"}, rows)
				}
				immediate := [][]any{{"b_immediate", int64(11), int64(900)}, {"m_transition", int64(11), int64(11)}}
				settled := [][]any{{"b_immediate", int64(11), int64(900)}, {"m_transition", int64(11), int64(11)}, {"a_deferred", int64(11), int64(900)}, {"z_deferred", int64(11), int64(900)}}
				if boundary == "immediate" {
					immediate = [][]any{{"a_deferred", int64(11), int64(900)}, {"b_immediate", int64(11), int64(900)}, {"m_transition", int64(11), int64(11)}, {"z_deferred", int64(11), int64(900)}}
					settled = immediate
				}
				run(fixture.Exec("BEGIN", 0))
				if boundary == "immediate" {
					run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
				}
				run(fixture.Select("DELETE FROM dt_parent WHERE id=1 RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				// RI exposure may run ordinary bodies, but cannot force deferred
				// constraint bodies or lend them the transition alias named x.
				run(audit(immediate...))
				run(fixture.Select("SELECT id FROM dt_when ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(11)}}))
				run(deferredCount("dt_parent", 0))
				run(deferredCount("dt_child", 0))
				if boundary == "set" || boundary == "savepoint" {
					if boundary == "savepoint" {
						run(fixture.Exec("SAVEPOINT queued", 0))
					}
					run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
					run(audit(settled...))
					if boundary == "savepoint" {
						run(fixture.Exec("ROLLBACK TO queued", 0))
						run(audit(immediate...))
						run(fixture.Exec("RELEASE queued", 0))
					}
				}
				run(fixture.Exec("COMMIT", 0))
				run(audit(settled...))
				// Restored consumption and terminal draining must not rerun WHEN.
				run(fixture.Select("SELECT id FROM dt_when ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(11)}}))
			})
		})
	}
}
