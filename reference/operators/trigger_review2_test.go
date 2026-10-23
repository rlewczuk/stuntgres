package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerCorePartitionReplace(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tr_replace(id integer,v integer) PARTITION BY RANGE(id)",
			"CREATE TABLE tr_replace_lo PARTITION OF tr_replace FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE tr_replace_hi PARTITION OF tr_replace FOR VALUES FROM(10) TO(20)",
			"CREATE FUNCTION tr_replace_triple() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v*3; RETURN NEW; END$$",
			"CREATE FUNCTION tr_replace_double() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v*2; RETURN NEW; END$$",
			"CREATE FUNCTION tr_replace_add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+7; RETURN NEW; END$$",
			"CREATE TRIGGER a BEFORE INSERT ON tr_replace_lo FOR EACH ROW EXECUTE FUNCTION tr_replace_triple()")
		insertPair := func(low, high, lowValue, highValue int64) {
			t.Helper()
			run(fixture.Exec(fmt.Sprintf("INSERT INTO tr_replace VALUES(%d,5),(%d,5)", low, high), 2))
			run(triggerRows(fmt.Sprintf("SELECT id,v FROM tr_replace WHERE id IN(%d,%d) ORDER BY id", low, high), []any{low, lowValue}, []any{high, highValue}))
		}
		// Ordinary CREATE cannot replace a local child definition or leave a
		// partially installed parent/other-child chain behind.
		run(fixture.Error("CREATE TRIGGER a BEFORE INSERT ON tr_replace FOR EACH ROW EXECUTE FUNCTION tr_replace_double()", "42710"))
		insertPair(1, 11, 15, 5)
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_replace FOR EACH ROW EXECUTE FUNCTION tr_replace_double()")
		insertPair(2, 12, 10, 10)
		// The replacement is a real parent-owned clone, not an independently
		// copied local definition that stops following later parent replacement.
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_replace FOR EACH ROW EXECUTE FUNCTION tr_replace_add()")
		insertPair(3, 13, 12, 12)
		run(fixture.Error("CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_replace FOR EACH ROW WHEN(OLD.id>0) EXECUTE FUNCTION tr_replace_double()", "42P17"))
		insertPair(4, 14, 12, 12)
		// Replacement authority belongs to that CREATE operation. ATTACH does
		// not inherit it, and failure preserves both the local trigger and tree.
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tr_replace_candidate(id integer,v integer)",
			"CREATE TRIGGER a BEFORE INSERT ON tr_replace_candidate FOR EACH ROW EXECUTE FUNCTION tr_replace_triple()")
		run(fixture.Error("ALTER TABLE tr_replace ATTACH PARTITION tr_replace_candidate FOR VALUES FROM(20) TO(30)", "42710"))
		run(fixture.Exec("INSERT INTO tr_replace_candidate VALUES(21,5)", 1))
		run(triggerRows("SELECT id,v FROM tr_replace_candidate", []any{int64(21), int64(15)}))
		run(fixture.Error("INSERT INTO tr_replace VALUES(22,5)", "23514"))
		run(triggerRows("SELECT id,v FROM tr_replace WHERE id>=20"))
	})
}

func TestReferenceTriggerCoreGeneratedUpdateOf(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				userOperatorExec(t, p, b, c,
					"CREATE TABLE tr_generated_of(id integer PRIMARY KEY,v integer,u integer,mark integer,g integer GENERATED ALWAYS AS(v*2) STORED)",
					"CREATE FUNCTION tr_generated_change() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+1; RETURN NEW; END$$",
					"CREATE FUNCTION tr_generated_mark() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.mark:=NEW.mark+1; RETURN NEW; END$$",
					"CREATE TRIGGER z BEFORE UPDATE OF g ON tr_generated_of FOR EACH ROW EXECUTE FUNCTION tr_generated_mark()")
				run(fixture.Exec("INSERT INTO tr_generated_of(id,v,u,mark) VALUES(1,5,0,0)", 1))
				update := func(query string, values []any, args ...any) {
					t.Helper()
					s := fixture.Select(query, []string{"v", "g", "u", "mark"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{values}, args...)
					s.Prepared = prepared
					run(s)
				}
				// g depends on the targeted v, so UPDATE OF g applies before final
				// generation, including an assignment that preserves v's value.
				update("UPDATE tr_generated_of SET v=$1 WHERE id=$2 RETURNING v,g,u,mark", []any{int64(6), int64(12), int64(0), int64(1)}, 6, 1)
				update("UPDATE tr_generated_of SET v=v WHERE id=1 RETURNING v,g,u,mark", []any{int64(6), int64(12), int64(0), int64(2)})
				// Even z itself is a BEFORE UPDATE hook: PostgreSQL includes g
				// for recomputation and column-specific firing on unrelated SET.
				update("UPDATE tr_generated_of SET u=$1 WHERE id=$2 RETURNING v,g,u,mark", []any{int64(6), int64(12), int64(9), int64(3)}, 9, 1)
				userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE UPDATE ON tr_generated_of FOR EACH ROW EXECUTE FUNCTION tr_generated_change()")
				update("UPDATE tr_generated_of SET u=$1 WHERE id=$2 RETURNING v,g,u,mark", []any{int64(7), int64(14), int64(10), int64(4)}, 10, 1)
				update("UPDATE tr_generated_of SET u=u WHERE id=1 RETURNING v,g,u,mark", []any{int64(8), int64(16), int64(10), int64(5)})
				run(fixture.Select("SELECT id,v,g,u,mark FROM tr_generated_of", []string{"id", "v", "g", "u", "mark"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(8), int64(16), int64(10), int64(5)}}))
			})
		})
	}
}
