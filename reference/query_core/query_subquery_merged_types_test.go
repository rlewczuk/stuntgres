package reference_test

import (
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQuerySubqueryMergedArrayProjection(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE merged_array(id INT,a INT[])", 0))
		run(fixture.Exec("INSERT INTO merged_array VALUES(1,'[0:1]={7,NULL}'),(2,'{}'),(3,NULL)", 3))
		q := "SELECT d.id,(SELECT i.a FROM merged_array i WHERE i.id=d.id) AS a FROM (SELECT id FROM merged_array) d ORDER BY d.id"
		check := fixture.ArrayReferenceSelect(q, []string{"id", "a"}, []string{"INT4", "_INT4"}, []string{"INT4", "_INT4"}, [][]any{{int64(1), "[0:1]={7,NULL}"}, {int64(2), "{}"}, {int64(3), nil}}, b.PG)
		subqueryBoth(run, check)
		subqueryBoth(run, fixture.ArrayReferenceSelect("SELECT (SELECT a FROM merged_array WHERE FALSE) AS a", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{nil}}, b.PG))
		heldSQL := "SELECT d.a,(SELECT $1::INT4[]) AS b FROM (SELECT $1::INT4[] AS a) d"
		stmt, err := c.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: heldSQL}
		for _, value := range []any{"{7,NULL}", "{}", nil, "[0:1]={8,9}"} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ArrayReferenceSelect(heldSQL, []string{"a", "b"}, []string{"_INT4", "_INT4"}, []string{"_INT4", "_INT4"}, [][]any{{value, value}}, b.PG, value))
		}
		run(fixture.Exec("CREATE VIEW merged_array_view AS "+q, 0))
		fixture.ViewCopy(t, p, b, "merged_array_view", "merged_array_copy")
		viewCheck := fixture.ArrayReferenceSelect("SELECT * FROM merged_array_copy ORDER BY id", check.Columns, check.Types, check.Types, check.Rows, b.PG)
		subqueryBoth(run, viewCheck)
		if !b.PG {
			dump := fixture.ViewDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.ViewRestore(t, p, dump)
			subqueryBoth(run, viewCheck)
		}
	})
}

func TestReferenceQuerySubqueryMergedEnumProjection(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TYPE merged_mood AS ENUM('sad','ok')", 0))
		run(fixture.Exec("CREATE TABLE merged_enum(id INT,v merged_mood)", 0))
		run(fixture.Exec("INSERT INTO merged_enum VALUES(1,'sad'),(2,'ok'),(3,NULL)", 3))
		queries := []fixture.Step{
			fixture.Select("SELECT (SELECT i.v FROM merged_enum i WHERE i.id=d.id) AS v FROM (SELECT id FROM merged_enum) d ORDER BY d.id", []string{"v"}, []string{""}, [][]any{{"sad"}, {"ok"}, {nil}}),
			fixture.Select("SELECT (SELECT v FROM merged_enum WHERE FALSE) AS v", []string{"v"}, []string{""}, [][]any{{nil}}),
			fixture.Select("SELECT d.v FROM (SELECT 'ok'::merged_mood AS v) d", []string{"v"}, []string{""}, [][]any{{"ok"}}),
		}
		for _, s := range queries {
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		q := "SELECT (SELECT $1::merged_mood) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, value := range []any{"sad", "ok", nil, "sad"} {
			s := fixture.Select(q, []string{"v"}, []string{""}, [][]any{{value}}, value)
			s.CheckMetadata = fixture.EnumReferenceMetadata(b.PG, false)
			fixture.EnumReferenceRunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("CREATE VIEW merged_enum_view AS "+queries[0].Query, 0))
		fixture.ViewCopy(t, p, b, "merged_enum_view", "merged_enum_copy")
		check := fixture.Select("SELECT * FROM merged_enum_copy", []string{"v"}, []string{""}, [][]any{{"sad"}, {"ok"}, {nil}})
		fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, check)
		if !b.PG {
			dump := fixture.ViewDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.ViewRestore(t, p, dump)
			fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, check)
		}
	})
}

func TestReferenceQuerySubqueryMergedProviderFrames(t *testing.T) {
	for _, tc := range []struct{ name, argument string }{
		{"format", "format('%s',t.id)"},
		{"index-property", "pg_indexam_has_property(403,CASE WHEN t.id=1 THEN 'can_order' ELSE 'can_unique' END)"},
		{"datetime-formatting", "to_char(CASE WHEN t.id=1 THEN TIMESTAMP '2020-01-01' ELSE TIMESTAMP '2020-02-01' END,'YYYY')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE a(id INT)", 0))
				run(fixture.Exec("INSERT INTO a VALUES(1)", 1))
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1),(2),(2)", 3))
				q := "SELECT t.id,(SELECT count(" + tc.argument + ") FROM a) AS n FROM t GROUP BY t.id ORDER BY t.id"
				want := fixture.Select(q, []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}})
				subqueryBoth(run, want)
				run(fixture.Exec("CREATE VIEW merged_provider_v AS "+q, 0))
				fixture.ViewCopy(t, p, b, "merged_provider_v", "merged_provider_copy")
				replay := fixture.Select("SELECT * FROM merged_provider_copy ORDER BY id", want.Columns, want.Types, want.Rows)
				subqueryBoth(run, replay)
				if !b.PG {
					dump := fixture.ViewDump(t, p)
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					fixture.ViewRestore(t, p, dump)
					subqueryBoth(run, replay)
				}
			})
		})
	}
}
