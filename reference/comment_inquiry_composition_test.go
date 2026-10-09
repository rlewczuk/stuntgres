package reference_test

import (
	"testing"
)

func TestReferenceCommentInquiryComposition(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Recorded", true: "Source"}[nonContext], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					db := backend.db
					run := func(s referenceStep) {
						t.Helper()
						s.nonContext = nonContext && !s.selectRows
						commentInquiryStep(t, p, backend, db, s)
					}
					run(referenceExec("CREATE TABLE t(a INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1),(1),(2)", 3))
					run(referenceExec("COMMENT ON TABLE t IS 'whole'", 0))
					grouped := referenceSelect("SELECT obj_description('t'::regclass,'pg_class') AS description,count(*) AS n FROM t GROUP BY obj_description('t'::regclass,'pg_class')", []string{"description", "n"}, []string{"TEXT", "INT8"}, [][]any{{"whole", int64(3)}})
					run(grouped)
					run(referencePrepared(grouped))
					if nonContext {
						source := referenceSelect("SELECT obj_description('t'::regclass,'pg_class'),count(*) FROM t GROUP BY obj_description('t'::regclass,'pg_class')", []string{"obj_description", "count"}, []string{"TEXT", "INT8"}, [][]any{{"whole", int64(3)}})
						source.nonContext = true
						commentInquiryStep(t, p, backend, db, source)
					}
					lazy := referenceSelect("SELECT CASE WHEN a=1 THEN obj_description('t'::regclass,'pg_class') ELSE col_description(NULL,1) END AS description FROM t ORDER BY a", []string{"description"}, []string{"TEXT"}, [][]any{{"whole"}, {"whole"}, {nil}})
					run(lazy)
					run(referencePrepared(lazy))
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
					run(referenceExec("CREATE SEQUENCE s", 0))
					run(referenceExec("COMMENT ON SEQUENCE s IS 'old'", 0))
					tx, err := db.Begin()
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					pending := referenceExec("COMMENT ON SEQUENCE s IS 'pending'", 0)
					pending.nonContext = nonContext
					commentInquiryStep(t, p, backend, tx, pending)
					rows, err := tx.Query("SELECT obj_description('s'::regclass,'pg_class')")
					commentInquiryRows(t, rows, err, backend.pg, false, "obj_description", []any{"pending"})
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					rows, err = db.Query("SELECT obj_description('s'::regclass,'pg_class')")
					commentInquiryRows(t, rows, err, backend.pg, false, "obj_description", []any{"old"})
				})
			}
		})
	}
}
