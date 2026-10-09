package reference_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceQuerySubqueryMergedArrayProjection(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(referenceExec("CREATE TABLE merged_array(id INT,a INT[])", 0))
		run(referenceExec("INSERT INTO merged_array VALUES(1,'[0:1]={7,NULL}'),(2,'{}'),(3,NULL)", 3))
		q := "SELECT d.id,(SELECT i.a FROM merged_array i WHERE i.id=d.id) AS a FROM (SELECT id FROM merged_array) d ORDER BY d.id"
		check := arrayReferenceSelect(q, []string{"id", "a"}, []string{"INT4", "_INT4"}, []string{"INT4", "_INT4"}, [][]any{{int64(1), "[0:1]={7,NULL}"}, {int64(2), "{}"}, {int64(3), nil}}, b.pg)
		subqueryBoth(run, check)
		subqueryBoth(run, arrayReferenceSelect("SELECT (SELECT a FROM merged_array WHERE FALSE) AS a", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{nil}}, b.pg))
		heldSQL := "SELECT d.a,(SELECT $1::INT4[]) AS b FROM (SELECT $1::INT4[] AS a) d"
		stmt, err := c.PrepareContext(p.ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: heldSQL}
		for _, value := range []any{"{7,NULL}", "{}", nil, "[0:1]={8,9}"} {
			referenceRunStep(t, p.ctx, held, b.recorder, arrayReferenceSelect(heldSQL, []string{"a", "b"}, []string{"_INT4", "_INT4"}, []string{"_INT4", "_INT4"}, [][]any{{value, value}}, b.pg, value))
		}
		run(referenceExec("CREATE VIEW merged_array_view AS "+q, 0))
		referenceViewCopy(t, p, b, "merged_array_view", "merged_array_copy")
		viewCheck := arrayReferenceSelect("SELECT * FROM merged_array_copy ORDER BY id", check.columns, check.types, check.types, check.rows, b.pg)
		subqueryBoth(run, viewCheck)
		if !b.pg {
			dump := referenceViewDump(t, p)
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			referenceViewRestore(t, p, dump)
			subqueryBoth(run, viewCheck)
		}
	})
}

func TestReferenceQuerySubqueryMergedEnumProjection(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(referenceExec("CREATE TYPE merged_mood AS ENUM('sad','ok')", 0))
		run(referenceExec("CREATE TABLE merged_enum(id INT,v merged_mood)", 0))
		run(referenceExec("INSERT INTO merged_enum VALUES(1,'sad'),(2,'ok'),(3,NULL)", 3))
		queries := []referenceStep{
			referenceSelect("SELECT (SELECT i.v FROM merged_enum i WHERE i.id=d.id) AS v FROM (SELECT id FROM merged_enum) d ORDER BY d.id", []string{"v"}, []string{""}, [][]any{{"sad"}, {"ok"}, {nil}}),
			referenceSelect("SELECT (SELECT v FROM merged_enum WHERE FALSE) AS v", []string{"v"}, []string{""}, [][]any{{nil}}),
			referenceSelect("SELECT d.v FROM (SELECT 'ok'::merged_mood AS v) d", []string{"v"}, []string{""}, [][]any{{"ok"}}),
		}
		for _, s := range queries {
			for _, prepared := range []bool{false, true} {
				s.prepared = prepared
				enumReferenceRunStep(t, p.ctx, c, b.recorder, s)
			}
		}
		q := "SELECT (SELECT $1::merged_mood) AS v"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, value := range []any{"sad", "ok", nil, "sad"} {
			s := referenceSelect(q, []string{"v"}, []string{""}, [][]any{{value}}, value)
			s.checkMetadata = enumReferenceMetadata(b.pg, false)
			enumReferenceRunStep(t, p.ctx, held, b.recorder, s)
		}
		run(referenceExec("CREATE VIEW merged_enum_view AS "+queries[0].query, 0))
		referenceViewCopy(t, p, b, "merged_enum_view", "merged_enum_copy")
		check := referenceSelect("SELECT * FROM merged_enum_copy", []string{"v"}, []string{""}, [][]any{{"sad"}, {"ok"}, {nil}})
		enumReferenceRunStep(t, p.ctx, c, b.recorder, check)
		if !b.pg {
			dump := referenceViewDump(t, p)
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			referenceViewRestore(t, p, dump)
			enumReferenceRunStep(t, p.ctx, c, b.recorder, check)
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
			subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				run(referenceExec("CREATE TABLE a(id INT)", 0))
				run(referenceExec("INSERT INTO a VALUES(1)", 1))
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1),(2),(2)", 3))
				q := "SELECT t.id,(SELECT count(" + tc.argument + ") FROM a) AS n FROM t GROUP BY t.id ORDER BY t.id"
				want := referenceSelect(q, []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}})
				subqueryBoth(run, want)
				run(referenceExec("CREATE VIEW merged_provider_v AS "+q, 0))
				referenceViewCopy(t, p, b, "merged_provider_v", "merged_provider_copy")
				replay := referenceSelect("SELECT * FROM merged_provider_copy ORDER BY id", want.columns, want.types, want.rows)
				subqueryBoth(run, replay)
				if !b.pg {
					dump := referenceViewDump(t, p)
					if err := stuntgres.Reset(p.ctx, p.native); err != nil {
						t.Fatal(err)
					}
					referenceViewRestore(t, p, dump)
					subqueryBoth(run, replay)
				}
			})
		})
	}
}
