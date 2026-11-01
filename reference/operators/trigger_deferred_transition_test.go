package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Deferred rows outlive the accepting query, not its transition-table window.
// Conversely, a nested SET must restore the still-running outer window after
// deferred bodies produce their own ordinary AFTER transition-table queries.
func TestReferenceTriggerDeferredTransition(t *testing.T) {
	for _, completion := range []string{"commit", "set", "nested"} {
		t.Run(completion, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				userOperatorExec(t, p, b, c,
					"CREATE TABLE dt_source(id integer)",
					"CREATE TABLE dt_pending(id integer)",
					"CREATE TABLE dt_sink(id integer)",
					"CREATE TABLE dt_audit(phase text,id integer)",
					"CREATE TABLE x(id integer)",
					"CREATE FUNCTION dt_sink_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO dt_audit SELECT 'inner',id FROM x; RETURN NULL; END$$",
					"CREATE TRIGGER s AFTER INSERT ON dt_sink REFERENCING NEW TABLE x EXECUTE FUNCTION dt_sink_body()",
					"CREATE FUNCTION dt_deferred_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO dt_audit VALUES('deferred',NEW.id); INSERT INTO dt_audit SELECT 'persistent',id FROM x; INSERT INTO dt_sink VALUES(NEW.id+100); RETURN NULL; END$$",
					"CREATE CONSTRAINT TRIGGER c AFTER INSERT ON dt_pending DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION dt_deferred_body()")
				settle := ""
				if completion == "nested" {
					settle = "SET CONSTRAINTS c IMMEDIATE; "
				}
				userOperatorExec(t, p, b, c,
					"CREATE FUNCTION dt_outer_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO dt_audit SELECT 'before',id FROM x; INSERT INTO dt_pending SELECT id FROM x; "+settle+"INSERT INTO dt_audit SELECT 'after',id FROM x; RETURN NULL; END$$",
					"CREATE TRIGGER o AFTER INSERT ON dt_source REFERENCING NEW TABLE x EXECUTE FUNCTION dt_outer_body()")
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				run(fixture.Exec("INSERT INTO x VALUES(900)", 1))
				run(fixture.Exec("BEGIN", 0))
				insert := "INSERT INTO dt_source VALUES($1),($2)"
				h, err := c.PrepareContext(p.Ctx, insert)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := h.Close(); err != nil {
						t.Error(err)
					}
				}()
				execute := func(a, z int64) {
					s := fixture.Exec(insert, 2, a, z)
					if prepared {
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: h, SQL: insert}, b.Recorder, s)
					} else {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
				rows := func(ids ...int64) [][]any {
					var out [][]any
					for _, id := range ids {
						out = append(out, []any{"before", id}, []any{"after", id})
					}
					return out
				}
				settled := func(ids ...int64) [][]any {
					out := rows(ids...)
					for _, id := range ids {
						out = append(out, []any{"deferred", id}, []any{"persistent", int64(900)}, []any{"inner", id + 100})
					}
					return out
				}
				audit := func(want [][]any) {
					s := fixture.Select("SELECT phase,id FROM dt_audit", []string{"phase", "id"}, []string{"TEXT", "INT4"}, want)
					s.Bag = true
					run(s)
				}
				execute(1, 2)
				if completion == "nested" {
					audit(settled(1, 2))
				} else {
					audit(rows(1, 2))
					run(deferredCount("dt_sink", 0))
				}
				if completion == "set" {
					run(fixture.Exec("SAVEPOINT consumed", 0))
					run(fixture.Exec("SET CONSTRAINTS c IMMEDIATE", 0))
					audit(settled(1, 2))
					run(fixture.Exec("ROLLBACK TO consumed", 0))
					audit(rows(1, 2))
					run(deferredCount("dt_sink", 0))
					run(fixture.Exec("RELEASE consumed", 0))
				}
				execute(3, 4)
				if completion != "nested" {
					audit(rows(1, 2, 3, 4))
				}
				run(fixture.Exec("COMMIT", 0))
				audit(settled(1, 2, 3, 4))
				run(fixture.Select("SELECT id FROM dt_sink ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(101)}, {int64(102)}, {int64(103)}, {int64(104)}}))
				run(fixture.Select("SELECT id FROM x", []string{"id"}, []string{"INT4"}, [][]any{{int64(900)}}))
			})
		})
	}
}
