package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerTransitionGrouping(t *testing.T) {
	t.Run("query-and-event", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE tt(id integer PRIMARY KEY,v integer)", "CREATE TABLE tt_other(id integer PRIMARY KEY,v integer)",
				"CREATE TABLE tt_group(rel text,op text,image text,id integer,v integer)", "CREATE TABLE tt_calls(seq integer GENERATED ALWAYS AS IDENTITY,rel text,op text,o bigint,n bigint)",
				"CREATE FUNCTION tt_group_i() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_group SELECT TG_TABLE_NAME,TG_OP,'NEW',id,v FROM x; INSERT INTO tt_calls(rel,op,o,n) VALUES(TG_TABLE_NAME,TG_OP,NULL,(SELECT count(*) FROM x)); RETURN NULL; END$$",
				"CREATE FUNCTION tt_group_u() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_group SELECT TG_TABLE_NAME,TG_OP,'OLD',id,v FROM y; INSERT INTO tt_group SELECT TG_TABLE_NAME,TG_OP,'NEW',id,v FROM x; INSERT INTO tt_calls(rel,op,o,n) VALUES(TG_TABLE_NAME,TG_OP,(SELECT count(*) FROM y),(SELECT count(*) FROM x)); RETURN NULL; END$$",
				"CREATE FUNCTION tt_group_d() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_group SELECT TG_TABLE_NAME,TG_OP,'OLD',id,v FROM y; INSERT INTO tt_calls(rel,op,o,n) VALUES(TG_TABLE_NAME,TG_OP,(SELECT count(*) FROM y),NULL); RETURN NULL; END$$",
				"CREATE TRIGGER i AFTER INSERT ON tt REFERENCING NEW TABLE x EXECUTE FUNCTION tt_group_i()",
				"CREATE TRIGGER u AFTER UPDATE ON tt REFERENCING OLD TABLE y NEW TABLE x EXECUTE FUNCTION tt_group_u()",
				"CREATE TRIGGER d AFTER DELETE ON tt REFERENCING OLD TABLE y EXECUTE FUNCTION tt_group_d()",
				"CREATE TRIGGER i AFTER INSERT ON tt_other REFERENCING NEW TABLE x EXECUTE FUNCTION tt_group_i()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			calls := func(rows ...[]any) fixture.Step {
				s := fixture.Select("SELECT rel,op,o,n FROM tt_calls", []string{"rel", "op", "o", "n"}, []string{"TEXT", "TEXT", "INT8", "INT8"}, rows)
				s.Bag = true
				return s
			}
			orderedCalls := func(rows ...[]any) fixture.Step {
				s := calls(rows...)
				s.Query += " ORDER BY seq"
				s.Bag = false
				return s
			}
			images := func(rows ...[]any) fixture.Step {
				s := fixture.Select("SELECT rel,op,image,id,v FROM tt_group", []string{"rel", "op", "image", "id", "v"}, []string{"TEXT", "TEXT", "TEXT", "INT4", "INT4"}, rows)
				s.Bag = true
				return s
			}
			run(fixture.Exec("WITH a AS(INSERT INTO tt VALUES(43,430)) INSERT INTO tt VALUES(44,440)", 1))
			run(calls([]any{"tt", "INSERT", nil, int64(2)}))
			run(images([]any{"tt", "INSERT", "NEW", int64(43), int64(430)}, []any{"tt", "INSERT", "NEW", int64(44), int64(440)}))
			userOperatorExec(t, p, b, c, "TRUNCATE tt_calls,tt_group")
			run(fixture.Exec("WITH a AS(INSERT INTO tt_other VALUES(45,450)) INSERT INTO tt VALUES(46,460)", 1))
			run(calls([]any{"tt", "INSERT", nil, int64(1)}, []any{"tt_other", "INSERT", nil, int64(1)}))
			run(images([]any{"tt_other", "INSERT", "NEW", int64(45), int64(450)}, []any{"tt", "INSERT", "NEW", int64(46), int64(460)}))
			run(fixture.Exec("INSERT INTO tt VALUES(1,10)", 1))
			userOperatorExec(t, p, b, c, "TRUNCATE tt_calls,tt_group")
			run(fixture.Exec("INSERT INTO tt VALUES(1,20),(2,30) ON CONFLICT(id) DO UPDATE SET v=excluded.v", 2))
			run(calls([]any{"tt", "INSERT", nil, int64(1)}, []any{"tt", "UPDATE", int64(1), int64(1)}))
			run(orderedCalls([]any{"tt", "UPDATE", int64(1), int64(1)}, []any{"tt", "INSERT", nil, int64(1)}))
			run(images([]any{"tt", "INSERT", "NEW", int64(2), int64(30)}, []any{"tt", "UPDATE", "OLD", int64(1), int64(10)}, []any{"tt", "UPDATE", "NEW", int64(1), int64(20)}))
			userOperatorExec(t, p, b, c, "TRUNCATE tt_calls,tt_group")
			run(fixture.Exec("INSERT INTO tt VALUES(3,40) ON CONFLICT(id) DO UPDATE SET v=excluded.v", 1))
			run(calls([]any{"tt", "INSERT", nil, int64(1)}, []any{"tt", "UPDATE", int64(0), int64(0)}))
			run(orderedCalls([]any{"tt", "UPDATE", int64(0), int64(0)}, []any{"tt", "INSERT", nil, int64(1)}))
			run(images([]any{"tt", "INSERT", "NEW", int64(3), int64(40)}))
			userOperatorExec(t, p, b, c, "TRUNCATE tt_calls,tt_group")
			run(fixture.Exec("INSERT INTO tt VALUES(1,999) ON CONFLICT DO NOTHING", 0))
			run(calls([]any{"tt", "INSERT", nil, int64(0)}))
			run(images())
			userOperatorExec(t, p, b, c, "TRUNCATE tt_calls,tt_group")
			mergeActions := " ON t.id=s.id WHEN MATCHED AND s.v=0 THEN DELETE WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,s.v)"
			merge := fixture.Exec("MERGE INTO tt t USING(VALUES(1,50),(2,0),(4,60)) s(id,v)"+mergeActions, 3)
			// pq does not expose PostgreSQL MERGE command-tag counts.
			if b.PG {
				merge.Affected = 0
			}
			run(merge)
			run(calls([]any{"tt", "INSERT", nil, int64(1)}, []any{"tt", "UPDATE", int64(1), int64(1)}, []any{"tt", "DELETE", int64(1), nil}))
			run(orderedCalls([]any{"tt", "DELETE", int64(1), nil}, []any{"tt", "UPDATE", int64(1), int64(1)}, []any{"tt", "INSERT", nil, int64(1)}))
			run(images([]any{"tt", "INSERT", "NEW", int64(4), int64(60)}, []any{"tt", "UPDATE", "OLD", int64(1), int64(20)}, []any{"tt", "UPDATE", "NEW", int64(1), int64(50)}, []any{"tt", "DELETE", "OLD", int64(2), int64(30)}))
			userOperatorExec(t, p, b, c, "TRUNCATE tt_calls,tt_group")
			run(fixture.Exec("MERGE INTO tt t USING(SELECT 99 AS id,99 AS v WHERE false) s"+mergeActions, 0))
			run(calls([]any{"tt", "DELETE", int64(0), nil}, []any{"tt", "UPDATE", int64(0), int64(0)}, []any{"tt", "INSERT", nil, int64(0)}))
			run(orderedCalls([]any{"tt", "DELETE", int64(0), nil}, []any{"tt", "UPDATE", int64(0), int64(0)}, []any{"tt", "INSERT", nil, int64(0)}))
			run(images())
			userOperatorExec(t, p, b, c, "TRUNCATE tt_calls,tt_group")
			copyDB := p.Native
			if b.PG {
				copyDB = p.CopyPostgres
			}
			copyConn, err := copyDB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer copyConn.Close()
			result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY tt(id,v) FROM STDIN", strings.NewReader("5\t70\n6\t80\n"))
			if err != nil || result.Count != 2 {
				t.Fatal(result, err)
			}
			run(calls([]any{"tt", "INSERT", nil, int64(2)}))
			run(images([]any{"tt", "INSERT", "NEW", int64(5), int64(70)}, []any{"tt", "INSERT", "NEW", int64(6), int64(80)}))
		})
	})
	t.Run("nested-same-target", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE tt(id integer)",
				"CREATE TABLE tt_nested(phase text,id integer,n bigint)",
				"CREATE FUNCTION tt_nested_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_nested SELECT 'before',id,(SELECT count(*) FROM x) FROM x; IF (SELECT min(id) FROM x)<100 THEN INSERT INTO tt SELECT id+100 FROM x; END IF; INSERT INTO tt_nested SELECT 'after',id,(SELECT count(*) FROM x) FROM x; RETURN NULL; END$$",
				"CREATE TRIGGER i AFTER INSERT ON tt REFERENCING NEW TABLE x EXECUTE FUNCTION tt_nested_body()")
			afterStep(t, p, b, c, prepared, fixture.Exec("INSERT INTO tt VALUES(1),(2)", 2))
			s := fixture.Select("SELECT phase,id,n FROM tt_nested", []string{"phase", "id", "n"}, []string{"TEXT", "INT4", "INT8"}, [][]any{
				{"before", int64(1), int64(2)}, {"before", int64(2), int64(2)}, {"after", int64(1), int64(2)}, {"after", int64(2), int64(2)},
				{"before", int64(101), int64(2)}, {"before", int64(102), int64(2)}, {"after", int64(101), int64(2)}, {"after", int64(102), int64(2)},
			})
			s.Bag = true
			afterStep(t, p, b, c, prepared, s)
			afterStep(t, p, b, c, prepared, fixture.Select("SELECT id FROM tt ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(101)}, {int64(102)}}))
		})
	})
}

func TestReferenceTriggerTransitionReferential(t *testing.T) {
	for _, expose := range []bool{false, true} {
		name := "statement-only"
		if expose {
			name = "freeze-rotate"
		}
		t.Run(name, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				userOperatorExec(t, p, b, c,
					"CREATE TABLE tt(id integer PRIMARY KEY,parent integer REFERENCES tt(id) ON DELETE CASCADE)",
					"CREATE TABLE tt_ri(seq integer GENERATED ALWAYS AS IDENTITY,level text,id integer,n bigint)",
					"CREATE TABLE tt_ri_images(level text,id integer)",
					"CREATE FUNCTION tt_ri_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_ri(level,id,n) VALUES(TG_LEVEL,OLD.id,(SELECT count(*) FROM x)); INSERT INTO tt_ri_images SELECT TG_LEVEL,id FROM x; RETURN NULL; END$$",
					"CREATE TRIGGER s AFTER DELETE ON tt REFERENCING OLD TABLE x EXECUTE FUNCTION tt_ri_body()")
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				if expose {
					userOperatorExec(t, p, b, c, "CREATE TRIGGER r AFTER DELETE ON tt REFERENCING OLD TABLE x FOR EACH ROW EXECUTE FUNCTION tt_ri_body()")
					run(fixture.Exec("INSERT INTO tt VALUES(1,NULL),(2,1),(3,2)", 3))
				} else {
					run(fixture.Exec("INSERT INTO tt VALUES(1,NULL),(2,1),(3,2),(4,3)", 4))
				}
				run(fixture.Exec("DELETE FROM tt WHERE id=1", 1))
				rows := [][]any{{"STATEMENT", nil, int64(4)}}
				images := [][]any{{"STATEMENT", int64(1)}, {"STATEMENT", int64(2)}, {"STATEMENT", int64(3)}, {"STATEMENT", int64(4)}}
				if expose {
					// Released trigger regression/source predicts two exposed segments.
					// Root must confirm exact ordering on pinned18.6 before native work.
					rows = [][]any{{"ROW", int64(1), int64(2)}, {"ROW", int64(2), int64(2)}, {"STATEMENT", nil, int64(2)}, {"ROW", int64(3), int64(1)}, {"STATEMENT", nil, int64(1)}}
					images = [][]any{{"ROW", int64(1)}, {"ROW", int64(2)}, {"ROW", int64(1)}, {"ROW", int64(2)}, {"STATEMENT", int64(1)}, {"STATEMENT", int64(2)}, {"ROW", int64(3)}, {"STATEMENT", int64(3)}}
				}
				run(fixture.Select("SELECT level,id,n FROM tt_ri ORDER BY seq", []string{"level", "id", "n"}, []string{"TEXT", "INT4", "INT8"}, rows))
				s := fixture.Select("SELECT level,id FROM tt_ri_images", []string{"level", "id"}, []string{"TEXT", "INT4"}, images)
				s.Bag = true
				run(s)
				run(fixture.Select("SELECT id FROM tt", []string{"id"}, []string{"INT4"}, nil))
			})
		})
	}
}
