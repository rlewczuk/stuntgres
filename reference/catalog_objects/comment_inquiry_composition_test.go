package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCommentInquiryComposition(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Recorded", true: "Source"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db := backend.DB
					run := func(s fixture.Step) {
						t.Helper()
						s.NonContext = nonContext && !s.SelectRows
						commentInquiryStep(t, p, backend, db, s)
					}
					run(fixture.Exec("CREATE TABLE t(a INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1),(1),(2)", 3))
					run(fixture.Exec("COMMENT ON TABLE t IS 'whole'", 0))
					grouped := fixture.Select("SELECT obj_description('t'::regclass,'pg_class') AS description,count(*) AS n FROM t GROUP BY obj_description('t'::regclass,'pg_class')", []string{"description", "n"}, []string{"TEXT", "INT8"}, [][]any{{"whole", int64(3)}})
					run(grouped)
					run(fixture.Prepared(grouped))
					if nonContext {
						source := fixture.Select("SELECT obj_description('t'::regclass,'pg_class'),count(*) FROM t GROUP BY obj_description('t'::regclass,'pg_class')", []string{"obj_description", "count"}, []string{"TEXT", "INT8"}, [][]any{{"whole", int64(3)}})
						source.NonContext = true
						commentInquiryStep(t, p, backend, db, source)
					}
					lazy := fixture.Select("SELECT CASE WHEN a=1 THEN obj_description('t'::regclass,'pg_class') ELSE col_description(NULL,1) END AS description FROM t ORDER BY a", []string{"description"}, []string{"TEXT"}, [][]any{{"whole"}, {"whole"}, {nil}})
					run(lazy)
					run(fixture.Prepared(lazy))
					original, err := db.Query("SELECT CASE WHEN a=1 THEN obj_description('t'::regclass,'pg_class') ELSE col_description(NULL,1) END FROM t ORDER BY a")
					if err != nil {
						t.Fatal(err)
					}
					defer original.Close()
					for _, want := range []any{"whole", "whole", nil} {
						if !original.Next() {
							t.Fatal("missing CASE row", original.Err())
						}
						var got any
						if err := original.Scan(&got); err != nil || got != want {
							t.Fatal(got, want, err)
						}
					}
					if original.Next() || original.Err() != nil {
						t.Fatal("CASE exhaustion", original.Err())
					}
					if err := original.Close(); err != nil {
						t.Fatal(err)
					}
					run(fixture.Exec("CREATE SEQUENCE s", 0))
					run(fixture.Exec("COMMENT ON SEQUENCE s IS 'old'", 0))
					tx, err := db.Begin()
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					pending := fixture.Exec("COMMENT ON SEQUENCE s IS 'pending'", 0)
					pending.NonContext = nonContext
					commentInquiryStep(t, p, backend, tx, pending)
					rows, err := tx.Query("SELECT obj_description('s'::regclass,'pg_class')")
					commentInquiryRows(t, rows, err, backend.PG, false, "obj_description", []any{"pending"})
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					rows, err = db.Query("SELECT obj_description('s'::regclass,'pg_class')")
					commentInquiryRows(t, rows, err, backend.PG, false, "obj_description", []any{"old"})
				})
			}
		})
	}
}
