package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// The same definitions drive ordinary commands and logical-control replay.
func transitionSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prefix string) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TABLE "+prefix+"tt(id integer PRIMARY KEY,v integer,g integer GENERATED ALWAYS AS(v*2) STORED)",
		"CREATE TABLE "+prefix+"tt_audit(op text,image text,id integer,v integer,g integer)",
		"CREATE TABLE "+prefix+"tt_count(op text,o bigint,n bigint)",
		"CREATE FUNCTION "+prefix+"tt_before() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+1; RETURN NEW; END$$",
		"CREATE FUNCTION "+prefix+"tt_insert() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO "+prefix+"tt_audit SELECT TG_OP,'NEW',id,v,g FROM \"New ' Rows\"; INSERT INTO "+prefix+"tt_count VALUES(TG_OP,NULL,(SELECT count(*) FROM \"New ' Rows\")); RETURN NULL; END$$",
		"CREATE FUNCTION "+prefix+"tt_update() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO "+prefix+"tt_audit SELECT TG_OP,'OLD',id,v,g FROM \"Old \"\" Rows\"; INSERT INTO "+prefix+"tt_audit SELECT TG_OP,'NEW',id,v,g FROM \"New ' Rows\"; INSERT INTO "+prefix+"tt_count VALUES(TG_OP,(SELECT count(*) FROM \"Old \"\" Rows\"),(SELECT count(*) FROM \"New ' Rows\")); RETURN NULL; END$$",
		"CREATE FUNCTION "+prefix+"tt_delete() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO "+prefix+"tt_audit SELECT TG_OP,'OLD',id,v,g FROM \"Old \"\" Rows\"; INSERT INTO "+prefix+"tt_count VALUES(TG_OP,(SELECT count(*) FROM \"Old \"\" Rows\"),NULL); RETURN NULL; END$$",
		"CREATE TRIGGER b BEFORE INSERT ON "+prefix+"tt FOR EACH ROW EXECUTE FUNCTION "+prefix+"tt_before()",
		"CREATE TRIGGER i AFTER INSERT ON "+prefix+"tt REFERENCING NEW TABLE AS \"New ' Rows\" FOR EACH STATEMENT EXECUTE FUNCTION "+prefix+"tt_insert()",
		"CREATE TRIGGER u AFTER UPDATE ON "+prefix+"tt REFERENCING NEW TABLE \"New ' Rows\" OLD TABLE AS \"Old \"\" Rows\" EXECUTE FUNCTION "+prefix+"tt_update()",
		"CREATE TRIGGER d AFTER DELETE ON "+prefix+"tt REFERENCING OLD TABLE \"Old \"\" Rows\" EXECUTE FUNCTION "+prefix+"tt_delete()")
}

func transitionRows(q string, rows ...[]any) fixture.Step {
	return fixture.Select(q, []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, rows)
}
func transitionAudit(prefix string, rows ...[]any) fixture.Step {
	s := fixture.Select("SELECT op,image,id,v,g FROM "+prefix+"tt_audit", []string{"op", "image", "id", "v", "g"}, []string{"TEXT", "TEXT", "INT4", "INT4", "INT4"}, rows)
	s.Bag = true
	return s
}
func transitionCounts(prefix string, rows ...[]any) fixture.Step {
	s := fixture.Select("SELECT op,o,n FROM "+prefix+"tt_count", []string{"op", "o", "n"}, []string{"TEXT", "INT8", "INT8"}, rows)
	s.Bag = true
	return s
}
func transitionSeedAudit() [][]any {
	return [][]any{{"INSERT", "NEW", int64(1), int64(11), int64(22)}, {"INSERT", "NEW", int64(2), int64(21), int64(42)}}
}

func TestReferenceTriggerTransitionImages(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		transitionSetup(t, p, b, c, "")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		s := transitionRows("INSERT INTO tt(id,v) VALUES(1,10),(2,20) RETURNING id,v,g", []any{int64(1), int64(11), int64(22)}, []any{int64(2), int64(21), int64(42)})
		s.Bag = true
		run(s)
		run(transitionAudit("", transitionSeedAudit()...))
		run(fixture.Exec("UPDATE tt SET v=v+5", 2))
		want := append(transitionSeedAudit(), []any{"UPDATE", "OLD", int64(1), int64(11), int64(22)}, []any{"UPDATE", "OLD", int64(2), int64(21), int64(42)}, []any{"UPDATE", "NEW", int64(1), int64(16), int64(32)}, []any{"UPDATE", "NEW", int64(2), int64(26), int64(52)})
		run(transitionAudit("", want...))
		run(fixture.Exec("UPDATE tt SET v=v", 2))
		want = append(want, []any{"UPDATE", "OLD", int64(1), int64(16), int64(32)}, []any{"UPDATE", "OLD", int64(2), int64(26), int64(52)}, []any{"UPDATE", "NEW", int64(1), int64(16), int64(32)}, []any{"UPDATE", "NEW", int64(2), int64(26), int64(52)})
		run(fixture.Exec("DELETE FROM tt", 2))
		want = append(want, []any{"DELETE", "OLD", int64(1), int64(16), int64(32)}, []any{"DELETE", "OLD", int64(2), int64(26), int64(52)})
		run(transitionAudit("", want...))
		run(transitionCounts("", []any{"INSERT", nil, int64(2)}, []any{"UPDATE", int64(2), int64(2)}, []any{"UPDATE", int64(2), int64(2)}, []any{"DELETE", int64(2), nil}))
		run(transitionRows("SELECT id,v,g FROM tt"))
	})
}

func TestReferenceTriggerTransitionMultiplicity(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tt(v integer)", "CREATE TABLE tt_copy(v integer)", "CREATE TABLE tt_scans(n bigint,again bigint,pairs bigint)",
			"CREATE FUNCTION tt_bag() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_copy SELECT v FROM x; INSERT INTO tt_scans VALUES((SELECT count(*) FROM x),(SELECT count(*) FROM x),(SELECT count(*) FROM x a CROSS JOIN x b)); RETURN NULL; END$$",
			"CREATE TRIGGER i AFTER INSERT ON tt REFERENCING NEW TABLE x EXECUTE FUNCTION tt_bag()")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("INSERT INTO tt VALUES(7),(7)", 2))
		run(fixture.Select("SELECT n,again,pairs FROM tt_scans", []string{"n", "again", "pairs"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(2), int64(2), int64(4)}}))
		run(fixture.Exec("INSERT INTO tt VALUES(NULL)", 1))
		s := fixture.Select("SELECT v FROM tt_copy", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {nil}})
		s.Bag = true
		run(s)
		userOperatorExec(t, p, b, c, "CREATE FUNCTION tt_suppress() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.v<0 THEN RETURN NULL; END IF; RETURN NEW; END$$", "CREATE TRIGGER b BEFORE INSERT ON tt FOR EACH ROW EXECUTE FUNCTION tt_suppress()")
		run(fixture.Exec("INSERT INTO tt VALUES(-1),(9)", 1))
		run(fixture.Select("SELECT n,again,pairs FROM tt_scans ORDER BY pairs,n", []string{"n", "again", "pairs"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(1), int64(1)}, {int64(2), int64(2), int64(4)}}))
		s.Rows = append(s.Rows, []any{int64(9)})
		run(s)
	})
}

func TestReferenceTriggerTransitionTiming(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tt(id integer)", "CREATE TABLE tt_log(seq integer GENERATED ALWAYS AS IDENTITY,name text,n bigint)",
			"CREATE TABLE tt_timing_images(name text,id integer)",
			"CREATE FUNCTION tt_timing() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_log(name,n) VALUES(TG_NAME,(SELECT count(*) FROM x)); INSERT INTO tt_timing_images SELECT TG_NAME,id FROM x; IF TG_LEVEL='ROW' THEN NEW.id:=999; END IF; RETURN NULL; END$$",
			"CREATE TRIGGER a AFTER INSERT ON tt REFERENCING NEW TABLE x FOR EACH ROW EXECUTE FUNCTION tt_timing()",
			"CREATE TRIGGER b AFTER INSERT ON tt REFERENCING NEW TABLE x FOR EACH ROW EXECUTE FUNCTION tt_timing()",
			"CREATE TRIGGER w AFTER INSERT ON tt REFERENCING NEW TABLE x FOR EACH ROW WHEN(NEW.id=1) EXECUTE FUNCTION tt_timing()",
			"CREATE TRIGGER f AFTER INSERT ON tt REFERENCING NEW TABLE x FOR EACH ROW WHEN(false) EXECUTE FUNCTION tt_timing()",
			"CREATE TRIGGER n AFTER INSERT ON tt REFERENCING NEW TABLE x FOR EACH ROW WHEN(NULL) EXECUTE FUNCTION tt_timing()",
			"CREATE TRIGGER s AFTER INSERT ON tt REFERENCING NEW TABLE x EXECUTE FUNCTION tt_timing()",
			"CREATE TRIGGER u AFTER UPDATE ON tt REFERENCING NEW TABLE x EXECUTE FUNCTION tt_timing()",
			"CREATE TRIGGER d AFTER DELETE ON tt REFERENCING OLD TABLE x EXECUTE FUNCTION tt_timing()")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("INSERT INTO tt VALUES(1),(2)", 2))
		run(fixture.Select("SELECT name,n FROM tt_log ORDER BY seq", []string{"name", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {"b", int64(2)}, {"w", int64(2)}, {"a", int64(2)}, {"b", int64(2)}, {"s", int64(2)}}))
		run(fixture.Select("SELECT id FROM tt ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		var images [][]any
		for _, name := range []string{"a", "b", "w", "a", "b", "s"} {
			images = append(images, []any{name, int64(1)}, []any{name, int64(2)})
		}
		imageStep := fixture.Select("SELECT name,id FROM tt_timing_images", []string{"name", "id"}, []string{"TEXT", "INT4"}, images)
		imageStep.Bag = true
		run(imageStep)
		userOperatorExec(t, p, b, c, "TRUNCATE tt_log,tt_timing_images", "CREATE FUNCTION tt_suppress() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$", "CREATE TRIGGER before_row BEFORE INSERT ON tt FOR EACH ROW EXECUTE FUNCTION tt_suppress()")
		run(fixture.Exec("INSERT INTO tt VALUES(3),(4)", 0))
		run(fixture.Select("UPDATE tt SET id=id WHERE false RETURNING id", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Select("DELETE FROM tt WHERE false RETURNING id", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Exec("INSERT INTO tt SELECT 5 WHERE false", 0))
		userOperatorExec(t, p, b, c, "CREATE TRIGGER before_other BEFORE UPDATE OR DELETE ON tt FOR EACH ROW EXECUTE FUNCTION tt_suppress()")
		run(fixture.Exec("UPDATE tt SET id=id", 0))
		run(fixture.Exec("DELETE FROM tt", 0))
		run(fixture.Select("SELECT name,n FROM tt_log ORDER BY seq", []string{"name", "n"}, []string{"TEXT", "INT8"}, [][]any{{"s", int64(0)}, {"u", int64(0)}, {"d", int64(0)}, {"s", int64(0)}, {"u", int64(0)}, {"d", int64(0)}}))
		run(fixture.Select("SELECT id FROM tt ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Select("SELECT name,id FROM tt_timing_images", []string{"name", "id"}, []string{"TEXT", "INT4"}, nil))
	})
}
