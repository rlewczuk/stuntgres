package reference_test

import "testing"

func TestReferenceQueryFromValuesComposition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
				for _, s := range []referenceStep{
					referenceExec("CREATE TABLE t(n INT,s TEXT)", 0),
					referenceExec("INSERT INTO t VALUES(1,'t'),(2,'u')", 2),
					referenceExec("CREATE TABLE empty(n INT)", 0),
					referenceExec("CREATE TABLE dst(n BIGINT,s TEXT)", 0),
				} {
					run(s)
				}
				for _, s := range []referenceStep{
					referenceSelect("SELECT l.n,r.n FROM (VALUES(1),(1)) l(n) CROSS JOIN (VALUES(2),(3)) r(n)", []string{"n", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(1), int64(2)}, {int64(1), int64(3)}, {int64(1), int64(3)}}),
					referenceSelect("SELECT t.n,v.s FROM t JOIN (VALUES(1,'a'),(1,'a'),(3,'c')) v(n,s) USING(n)", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}}),
					referenceSelect("SELECT t.n,v.s FROM (VALUES(1,'a'),(1,'a'),(3,'c')) v(n,s) JOIN t ON t.n=v.n", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}}),
					referenceSelect("SELECT n FROM t NATURAL JOIN (VALUES(1),(1),(3)) v(n)", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}),
					referenceSelect("SELECT u.n FROM t JOIN (VALUES(1),(1),(3)) v(n) USING(n) AS u", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}),
					referenceSelect("SELECT j.n FROM (t JOIN (VALUES(1),(1),(3)) v(n) USING(n)) j", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}),
					referenceSelect("SELECT empty.n,v.n FROM empty CROSS JOIN (VALUES(1),(NULL)) v(n)", []string{"n", "n"}, []string{"INT4", "INT4"}, [][]any{}),
					referenceSelect("SELECT v.n,empty.n FROM (VALUES(1),(NULL)) v(n) CROSS JOIN empty", []string{"n", "n"}, []string{"INT4", "INT4"}, [][]any{}),
				} {
					s.prepared, s.bag = prepared, true
					run(s)
				}
				s := referenceExec("INSERT INTO dst SELECT * FROM (VALUES(1,'a'),(1,'a'),(2,NULL)) v(n,s)", 3)
				s.prepared = prepared
				run(s)
				s = referenceSelect("SELECT * FROM dst", []string{"n", "s"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}})
				s.bag = true
				run(s)
			}
		})
	}
}

func TestReferenceQueryFromValuesViews(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		for _, tc := range []struct {
			name, query    string
			columns, types []string
			rows           [][]any
		}{
			{"q", "SELECT * FROM (VALUES(1,'a'),(1,'a'),(2,NULL)) v(n,s)", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}}},
			{"bare_values", "VALUES(1),(NULL)", []string{"column1"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}}},
			{"multi_values", "SELECT l.n,r.s FROM (VALUES(2),(1) ORDER BY column1 OFFSET 1 LIMIT 1) l(n) CROSS JOIN (VALUES('a'::char(2)),(NULL::char(2)) ORDER BY column1 LIMIT 1) r(s)", []string{"n", "s"}, []string{"INT4", "BPCHAR"}, [][]any{{int64(2), "a "}}},
		} {
			run(referenceExec("CREATE VIEW "+tc.name+" AS "+tc.query, 0))
			s := referenceSelect("SELECT * FROM "+tc.name, tc.columns, tc.types, tc.rows)
			s.bag = true
			run(s)
			var definition string
			if err := backend.db.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1", tc.name).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			// Native formatting may differ; both definitions must execute to
			// the independently specified schema and occurrence bag below.
			replay := referenceSelect(definition, tc.columns, tc.types, tc.rows)
			replay.bag = true
			run(replay)
		}
		run(referenceExec("CREATE OR REPLACE VIEW q AS SELECT * FROM (VALUES(3,'new'),(NULL,NULL)) v(n,s)", 0))
		s := referenceSelect("SELECT * FROM q", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "new"}, {nil, nil}})
		s.bag = true
		run(s)
	}
}
