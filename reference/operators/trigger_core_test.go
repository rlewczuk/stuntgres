package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// One shared backend loop also gives oracle discovery a single temporary filter
// site. Normal runs deliberately exercise every configured backend.
func triggerScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func triggerRows(q string, rows ...[]any) fixture.Step {
	return fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, rows)
}

func triggerSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TABLE tr_data(id integer PRIMARY KEY,v integer)",
		"CREATE FUNCTION tr_add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v+TG_ARGV[0]::integer; RETURN NEW; END$$",
		"CREATE FUNCTION tr_double() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v*2; RETURN NEW; END$$",
		"CREATE FUNCTION tr_gate() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN CASE WHEN COALESCE(NEW.id,OLD.id)=2 THEN NULL ELSE COALESCE(NEW,OLD) END; END$$")
}

func TestReferenceTriggerCoreChain(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerSetup(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"CREATE TRIGGER z BEFORE INSERT ON tr_data FOR EACH ROW WHEN(NEW.v=8) EXECUTE FUNCTION tr_double()",
			"CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(3)")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(16)}))
		run(fixture.Prepared(triggerRows("INSERT INTO tr_data VALUES(2,5) RETURNING id,v", []any{int64(2), int64(16)})))
		run(triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(1), int64(16)}, []any{int64(2), int64(16)}))
	})
}

func TestReferenceTriggerCoreDefinition(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE FUNCTION tr_add(x integer) RETURNS integer LANGUAGE SQL AS 'SELECT x+1'", "CREATE FUNCTION tr_wrong() RETURNS integer LANGUAGE SQL AS 'SELECT 1'")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE PROCEDURE tr_add('3')")
		run(fixture.ErrorDiagnostic("CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()", fixture.ControlsReferenceDiagnostic("42710", `trigger "a" for relation "tr_data" already exists`)))
		run(fixture.Error("CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_wrong()", "42P17"))
		run(triggerRows("INSERT INTO tr_data VALUES(9,5) RETURNING id,v", []any{int64(9), int64(8)}))
		run(fixture.Exec("DELETE FROM tr_data WHERE id=9", 1))
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER a BEFORE UPDATE ON tr_data FOR EACH ROW WHEN(NEW.id=1) EXECUTE FUNCTION tr_add(7)")
		run(fixture.Exec("INSERT INTO tr_data VALUES(1,5),(2,5)", 2))
		run(fixture.Exec("UPDATE tr_data SET v=5", 2))
		run(triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(1), int64(12)}, []any{int64(2), int64(5)}))
		run(fixture.Error("CREATE OR REPLACE TRIGGER a BEFORE UPDATE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_missing()", "42883"))
		run(triggerRows("UPDATE tr_data SET v=5 WHERE id=1 RETURNING id,v", []any{int64(1), int64(12)}))
		userOperatorExec(t, p, b, c, "CREATE TABLE tr_other(id integer,v integer)", "CREATE TRIGGER a BEFORE INSERT ON tr_other FOR EACH ROW EXECUTE FUNCTION tr_double()")
		run(triggerRows("INSERT INTO tr_other VALUES(1,5) RETURNING id,v", []any{int64(1), int64(10)}))
	})
}

func TestReferenceTriggerCoreSuppression(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerSetup(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION tr_late() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := 100/(NEW.id-2); RETURN NEW; END$$",
			"CREATE TRIGGER a BEFORE INSERT OR UPDATE OR DELETE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_gate()",
			"CREATE TRIGGER z BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_late()")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		s := triggerRows("INSERT INTO tr_data VALUES(1,5),(2,5),(3,5) RETURNING id,v", []any{int64(1), int64(-100)}, []any{int64(3), int64(100)})
		s.Bag = true
		run(s)
		run(fixture.Exec("INSERT INTO tr_data VALUES(2,5)", 0))
		run(fixture.Prepared(triggerRows("INSERT INTO tr_data VALUES(2,5) RETURNING id,v")))
		run(fixture.Exec("UPDATE tr_data SET v=v WHERE id=99", 0))
		run(fixture.Exec("DELETE FROM tr_data WHERE id=99", 0))
		run(fixture.Exec("UPDATE tr_data SET v=v", 2))
		run(fixture.Exec("DELETE FROM tr_data", 2))
		run(triggerRows("SELECT id,v FROM tr_data ORDER BY id"))
	})
}

func TestReferenceTriggerCoreRows(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerSetup(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION tr_rows() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := OLD.v+NEW.v; RETURN NEW; END$$",
			"CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_rows()")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(triggerRows("INSERT INTO tr_data VALUES(4,5) RETURNING id,v", []any{int64(4), nil}))
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER a BEFORE UPDATE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_rows()")
		run(fixture.Exec("INSERT INTO tr_data VALUES(1,10),(2,20),(3,30)", 3))
		userOperatorExec(t, p, b, c, "CREATE TRIGGER b BEFORE UPDATE OR DELETE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_gate()")
		run(fixture.Exec("UPDATE tr_data SET v=5 WHERE id<4", 2))
		run(triggerRows("SELECT id,v FROM tr_data WHERE id<4 ORDER BY id", []any{int64(1), int64(15)}, []any{int64(2), int64(20)}, []any{int64(3), int64(35)}))
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION tr_delete_mutate() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN OLD.v := 999; RETURN OLD; END$$",
			"CREATE FUNCTION tr_delete_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN OLD.v := 1/0; RETURN OLD; END$$",
			"CREATE OR REPLACE TRIGGER a BEFORE DELETE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_delete_mutate()",
			"CREATE TRIGGER z BEFORE DELETE ON tr_data FOR EACH ROW WHEN(OLD.v=999) EXECUTE FUNCTION tr_delete_fail()")
		s := triggerRows("DELETE FROM tr_data WHERE id<4 RETURNING id,v", []any{int64(1), int64(15)}, []any{int64(3), int64(35)})
		s.Bag = true
		run(s)
		userOperatorExec(t, p, b, c, "CREATE FUNCTION tr_delete_new() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "CREATE OR REPLACE TRIGGER a BEFORE DELETE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_delete_new()")
		run(fixture.Exec("DELETE FROM tr_data WHERE id=4", 0))
	})
}

func TestReferenceTriggerCoreFrame(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tr_frame(id integer,label text,n integer,ok boolean)",
			"CREATE FUNCTION tr_frame_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.label := TG_ARGV[0]||':'||TG_ARGV[1]||':'||TG_OP; NEW.n := TG_NARGS; NEW.ok := TG_WHEN='BEFORE' AND TG_LEVEL='ROW' AND TG_NAME='frame' AND TG_TABLE_NAME='tr_frame' AND TG_TABLE_SCHEMA='public' AND TG_RELNAME=TG_TABLE_NAME AND TG_RELID='tr_frame'::regclass::oid AND TG_ARGV[-1] IS NULL AND TG_ARGV[TG_NARGS] IS NULL; RETURN NEW; END$$",
			"CREATE TRIGGER frame BEFORE INSERT ON tr_frame FOR EACH ROW EXECUTE FUNCTION tr_frame_fn('x',7)")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Select("INSERT INTO tr_frame(id) VALUES(1) RETURNING label,n,ok", []string{"label", "n", "ok"}, []string{"TEXT", "INT4", "BOOL"}, [][]any{{"x:7:INSERT", int64(2), true}}))
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER frame BEFORE INSERT ON tr_frame FOR EACH ROW EXECUTE FUNCTION tr_frame_fn()")
		run(fixture.Select("INSERT INTO tr_frame(id) VALUES(2) RETURNING label,n,ok", []string{"label", "n", "ok"}, []string{"TEXT", "INT4", "BOOL"}, [][]any{{nil, int64(0), true}}))
		userOperatorExec(t, p, b, c, "CREATE TABLE tr_shape(id integer,label text,n integer,ok boolean,extra text)", "CREATE TRIGGER frame BEFORE INSERT ON tr_shape FOR EACH ROW EXECUTE FUNCTION tr_frame_fn('y',8)")
		run(fixture.Select("INSERT INTO tr_shape(id,extra) VALUES(3,'kept') RETURNING label,n,ok,extra", []string{"label", "n", "ok", "extra"}, []string{"TEXT", "INT4", "BOOL", "TEXT"}, [][]any{{"y:8:INSERT", int64(2), false, "kept"}}))
	})
}

func TestReferenceTriggerCoreWhen(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerSetup(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"CREATE TRIGGER a BEFORE UPDATE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(3)",
			"CREATE TRIGGER z BEFORE UPDATE OF v ON tr_data FOR EACH ROW WHEN(NEW.v=8) EXECUTE FUNCTION tr_double()",
			"CREATE TRIGGER f BEFORE INSERT ON tr_data FOR EACH ROW WHEN(FALSE) EXECUTE FUNCTION tr_double()",
			"CREATE TRIGGER n BEFORE INSERT ON tr_data FOR EACH ROW WHEN(NULL) EXECUTE FUNCTION tr_double()")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("INSERT INTO tr_data VALUES(1,5)", 1))
		run(triggerRows("UPDATE tr_data SET id=id RETURNING id,v", []any{int64(1), int64(8)}))
		run(triggerRows("UPDATE tr_data SET v=5 RETURNING id,v", []any{int64(1), int64(16)}))
		run(triggerRows("UPDATE tr_data SET v=v RETURNING id,v", []any{int64(1), int64(19)}))
		run(fixture.Error("CREATE TRIGGER bad BEFORE INSERT ON tr_data FOR EACH ROW WHEN(OLD.v=1) EXECUTE FUNCTION tr_double()", "42P17"))
		run(fixture.Error("CREATE TRIGGER bad BEFORE DELETE ON tr_data FOR EACH ROW WHEN(NEW.v=1) EXECUTE FUNCTION tr_double()", "42P17"))
		run(fixture.Error("CREATE TRIGGER bad BEFORE INSERT ON tr_data FOR EACH ROW WHEN((SELECT true)) EXECUTE FUNCTION tr_double()", "0A000"))
	})
}
