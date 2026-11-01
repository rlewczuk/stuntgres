package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations follow CREATE TRIGGER's hierarchy projection rule and released
// ExecDeleteEpilogue/ExecInsert: movement collects OLD before destination veto.
func TestReferenceTriggerTransitionHierarchy(t *testing.T) {
	t.Run("inheritance", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE tt(id integer,v integer)",
				"CREATE TABLE tt_child(extra text) INHERITS(tt)",
				"CREATE TABLE tt_images(op text,image text,row_text text)",
				"CREATE FUNCTION tt_u() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_images SELECT TG_OP,'OLD',y::text FROM y; INSERT INTO tt_images SELECT TG_OP,'NEW',x::text FROM x; RETURN NULL; END$$",
				"CREATE FUNCTION tt_d() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_images SELECT TG_OP,'OLD',y::text FROM y; RETURN NULL; END$$",
				"CREATE TRIGGER u AFTER UPDATE ON tt REFERENCING OLD TABLE y NEW TABLE x EXECUTE FUNCTION tt_u()",
				"CREATE TRIGGER d AFTER DELETE ON tt REFERENCING OLD TABLE y EXECUTE FUNCTION tt_d()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("INSERT INTO tt VALUES(1,10)", 1))
			run(fixture.Exec("INSERT INTO tt_child VALUES(2,20,'child-only')", 1))
			run(fixture.Select("UPDATE tt SET v=v+1 FROM(VALUES(1)) q(k) WHERE q.k=1 RETURNING id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(21)}}))
			run(fixture.Select("DELETE FROM tt USING(VALUES(1)) q(k) WHERE q.k=1 RETURNING id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(21)}}))
			s := fixture.Select("SELECT op,image,row_text FROM tt_images", []string{"op", "image", "row_text"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{
				{"UPDATE", "OLD", "(1,10)"}, {"UPDATE", "OLD", "(2,20)"}, {"UPDATE", "NEW", "(1,11)"}, {"UPDATE", "NEW", "(2,21)"}, {"DELETE", "OLD", "(1,11)"}, {"DELETE", "OLD", "(2,21)"},
			})
			s.Bag = true
			run(s)
			userOperatorExec(t, p, b, c, "TRUNCATE tt_images",
				"CREATE TRIGGER d AFTER DELETE ON tt_child REFERENCING OLD TABLE y EXECUTE FUNCTION tt_d()")
			run(fixture.Exec("INSERT INTO tt VALUES(1,10)", 1))
			run(fixture.Exec("INSERT INTO tt_child VALUES(2,20,'child-only')", 1))
			run(fixture.Exec("DELETE FROM ONLY tt", 1))
			run(fixture.Select("SELECT op,image,row_text FROM tt_images", []string{"op", "image", "row_text"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"DELETE", "OLD", "(1,10)"}}))
			userOperatorExec(t, p, b, c, "TRUNCATE tt_images")
			run(fixture.Exec("DELETE FROM tt_child", 1))
			run(fixture.Select("SELECT op,image,row_text FROM tt_images", []string{"op", "image", "row_text"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"DELETE", "OLD", "(2,20,child-only)"}}))
		})
	})
	t.Run("routing-movement-veto", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE tt(id integer,v integer) PARTITION BY RANGE(id)",
				"CREATE TABLE tt_low PARTITION OF tt FOR VALUES FROM(0) TO(10)",
				"CREATE TABLE tt_high(v integer,id integer)",
				"ALTER TABLE tt ATTACH PARTITION tt_high FOR VALUES FROM(10) TO(100)",
				"CREATE TABLE tt_images(op text,image text,id integer,v integer)",
				"CREATE TABLE tt_rows(rel text,op text)",
				"CREATE TABLE tt_calls(op text,o bigint,n bigint)",
				"CREATE FUNCTION tt_i() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_images SELECT TG_OP,'NEW',id,v FROM x; INSERT INTO tt_calls VALUES(TG_OP,NULL,(SELECT count(*) FROM x)); RETURN NULL; END$$",
				"CREATE FUNCTION tt_u() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_images SELECT TG_OP,'OLD',id,v FROM y; INSERT INTO tt_images SELECT TG_OP,'NEW',id,v FROM x; INSERT INTO tt_calls VALUES(TG_OP,(SELECT count(*) FROM y),(SELECT count(*) FROM x)); RETURN NULL; END$$",
				"CREATE FUNCTION tt_d() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_images SELECT TG_OP,'OLD',id,v FROM y; INSERT INTO tt_calls VALUES(TG_OP,(SELECT count(*) FROM y),NULL); RETURN NULL; END$$",
				"CREATE FUNCTION tt_row() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_rows VALUES(TG_TABLE_NAME,TG_OP); RETURN NULL; END$$",
				"CREATE FUNCTION tt_veto_i() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.v=99 THEN RETURN NULL; END IF; IF NEW.v=98 THEN PERFORM 1/0; END IF; NEW.v:=NEW.v+1; RETURN NEW; END$$",
				"CREATE FUNCTION tt_veto_d() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF OLD.v=31 THEN RETURN NULL; END IF; RETURN OLD; END$$",
				"CREATE FUNCTION tt_veto_u() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.v=77 THEN RETURN NULL; END IF; RETURN NEW; END$$",
				"CREATE TRIGGER i AFTER INSERT ON tt REFERENCING NEW TABLE x EXECUTE FUNCTION tt_i()",
				"CREATE TRIGGER u AFTER UPDATE ON tt REFERENCING OLD TABLE y NEW TABLE x EXECUTE FUNCTION tt_u()",
				"CREATE TRIGGER d AFTER DELETE ON tt REFERENCING OLD TABLE y EXECUTE FUNCTION tt_d()",
				"CREATE TRIGGER r AFTER INSERT OR UPDATE OR DELETE ON tt FOR EACH ROW EXECUTE FUNCTION tt_row()",
				"CREATE TRIGGER b_i BEFORE INSERT ON tt FOR EACH ROW EXECUTE FUNCTION tt_veto_i()",
				"CREATE TRIGGER b_d BEFORE DELETE ON tt FOR EACH ROW EXECUTE FUNCTION tt_veto_d()")
			userOperatorExec(t, p, b, c, "CREATE TRIGGER b_u BEFORE UPDATE ON tt FOR EACH ROW EXECUTE FUNCTION tt_veto_u()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Select("INSERT INTO tt VALUES(1,10),(2,20),(3,30),(4,99) RETURNING id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(21)}, {int64(3), int64(31)}}))
			run(fixture.Exec("UPDATE tt SET id=id+10 WHERE id=1", 1))
			// Source deletion survives a destination INSERT veto, without a NEW image.
			run(fixture.Exec("UPDATE tt SET id=id+10,v=99 WHERE id=2", 0))
			// A source DELETE veto admits neither side of the logical UPDATE.
			run(fixture.Exec("UPDATE tt SET id=id+10 WHERE id=3", 0))
			run(fixture.Exec("UPDATE tt SET id=id+10,v=77 WHERE id=3", 0))
			s := fixture.Select("SELECT op,image,id,v FROM tt_images", []string{"op", "image", "id", "v"}, []string{"TEXT", "TEXT", "INT4", "INT4"}, [][]any{
				{"INSERT", "NEW", int64(1), int64(11)}, {"INSERT", "NEW", int64(2), int64(21)}, {"INSERT", "NEW", int64(3), int64(31)},
				{"UPDATE", "OLD", int64(1), int64(11)}, {"UPDATE", "NEW", int64(11), int64(12)}, {"UPDATE", "OLD", int64(2), int64(21)},
			})
			s.Bag = true
			run(s)
			s = fixture.Select("SELECT rel,op FROM tt_rows", []string{"rel", "op"}, []string{"TEXT", "TEXT"}, [][]any{
				{"tt_low", "INSERT"}, {"tt_low", "INSERT"}, {"tt_low", "INSERT"}, {"tt_low", "DELETE"}, {"tt_high", "INSERT"}, {"tt_low", "DELETE"},
			})
			s.Bag = true
			run(s)
			s = fixture.Select("SELECT op,o,n FROM tt_calls", []string{"op", "o", "n"}, []string{"TEXT", "INT8", "INT8"}, [][]any{
				{"INSERT", nil, int64(3)}, {"UPDATE", int64(1), int64(1)}, {"UPDATE", int64(1), int64(0)}, {"UPDATE", int64(0), int64(0)}, {"UPDATE", int64(0), int64(0)},
			})
			s.Bag = true
			run(s)
			run(fixture.Select("SELECT id,v FROM tt ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(31)}, {int64(11), int64(12)}}))
			run(fixture.Exec("INSERT INTO tt VALUES(5,40)", 1))
			userOperatorExec(t, p, b, c, "TRUNCATE tt_images,tt_calls,tt_rows")
			want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
			want.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function tt_veto_i() line 1 at PERFORM"
			run(fixture.ErrorDiagnostic("UPDATE tt SET id=id+10,v=98 WHERE id=5", want))
			run(fixture.Select("SELECT id,v FROM tt ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(31)}, {int64(5), int64(41)}, {int64(11), int64(12)}}))
			run(fixture.Select("SELECT op FROM tt_calls", []string{"op"}, []string{"TEXT"}, nil))
			run(fixture.Select("SELECT op FROM tt_images", []string{"op"}, []string{"TEXT"}, nil))
			run(fixture.Select("SELECT op FROM tt_rows", []string{"op"}, []string{"TEXT"}, nil))
		})
	})
	t.Run("batch-and-unmoved", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE tt(id integer,v integer) PARTITION BY RANGE(id)",
				"CREATE TABLE tt_low PARTITION OF tt FOR VALUES FROM(0) TO(10)",
				"CREATE TABLE tt_high(v integer,id integer)",
				"ALTER TABLE tt ATTACH PARTITION tt_high FOR VALUES FROM(10) TO(100)",
				"CREATE TABLE tt_images(op text,image text,row_text text)",
				"CREATE TABLE tt_leaf_calls(op text)",
				"CREATE FUNCTION tt_i() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_images SELECT TG_OP,'NEW',x::text FROM x; RETURN NULL; END$$",
				"CREATE FUNCTION tt_u() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_images SELECT TG_OP,'OLD',y::text FROM y; INSERT INTO tt_images SELECT TG_OP,'NEW',x::text FROM x; RETURN NULL; END$$",
				"CREATE FUNCTION tt_d() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_images SELECT TG_OP,'OLD',y::text FROM y; RETURN NULL; END$$",
				"CREATE FUNCTION tt_leaf() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_leaf_calls VALUES(TG_OP); RETURN NULL; END$$",
				"CREATE TRIGGER i AFTER INSERT ON tt REFERENCING NEW TABLE x EXECUTE FUNCTION tt_i()",
				"CREATE TRIGGER u AFTER UPDATE ON tt REFERENCING OLD TABLE y NEW TABLE x EXECUTE FUNCTION tt_u()",
				"CREATE TRIGGER d AFTER DELETE ON tt REFERENCING OLD TABLE y EXECUTE FUNCTION tt_d()",
				"CREATE TRIGGER s AFTER INSERT OR UPDATE OR DELETE ON tt_high EXECUTE FUNCTION tt_leaf()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("INSERT INTO tt VALUES(1,10),(11,20)", 2))
			run(fixture.Exec("UPDATE tt SET v=v+1", 2))
			s := fixture.Select("SELECT op,image,row_text FROM tt_images", []string{"op", "image", "row_text"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{
				{"INSERT", "NEW", "(1,10)"}, {"INSERT", "NEW", "(11,20)"},
				{"UPDATE", "OLD", "(1,10)"}, {"UPDATE", "OLD", "(11,20)"}, {"UPDATE", "NEW", "(1,11)"}, {"UPDATE", "NEW", "(11,21)"},
			})
			s.Bag = true
			run(s)
			run(fixture.Select("SELECT op FROM tt_leaf_calls", []string{"op"}, []string{"TEXT"}, nil))
			run(fixture.Select("SELECT id,v FROM tt ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(11), int64(21)}}))
		})
	})
}

func TestReferenceTriggerTransitionCorrelatedPresence(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tt(id integer,v integer)", "CREATE TABLE tt_presence(k integer,row_text text)",
			"CREATE FUNCTION tt_presence_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_presence SELECT v.k,n::text FROM (VALUES(0),(1)) v(k) LEFT JOIN (x n CROSS JOIN LATERAL generate_series(1,v.k) g(i)) ON true; RETURN NULL; END$$",
			"CREATE TRIGGER i AFTER INSERT ON tt REFERENCING NEW TABLE x EXECUTE FUNCTION tt_presence_body()")
		afterStep(t, p, b, c, prepared, fixture.Exec("INSERT INTO tt VALUES(NULL,NULL)", 1))
		afterStep(t, p, b, c, prepared, fixture.Select("SELECT k,row_text FROM tt_presence ORDER BY k", []string{"k", "row_text"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), nil}, {int64(1), "(,)"}}))
		const query = "INSERT INTO tt VALUES($1,$2)"
		statement, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
		for _, tc := range []struct {
			a, b any
			text string
		}{{nil, nil, "(,)"}, {int64(7), int64(8), "(7,8)"}} {
			userOperatorExec(t, p, b, c, "TRUNCATE tt_presence")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, tc.a, tc.b))
			afterStep(t, p, b, c, prepared, fixture.Select("SELECT k,row_text FROM tt_presence ORDER BY k", []string{"k", "row_text"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), nil}, {int64(1), tc.text}}))
		}
	})
}
