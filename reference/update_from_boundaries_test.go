package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceUpdateFromBoundaries(t *testing.T) {
	cases := []struct {
		name, q                            string
		count                              int64
		rows                               [][]any
		code, message, token, detail, hint string
	}{
		{name: "left", q: "UPDATE t x SET v=r.z FROM s LEFT JOIN r USING(k) WHERE x.id=s.k", count: 2, rows: [][]any{{int64(1), int64(9)}, {int64(2), nil}, {int64(3), int64(30)}}},
		{name: "right", q: "UPDATE t x SET v=r.z FROM s RIGHT JOIN r USING(k) WHERE x.id=s.k", count: 1, rows: [][]any{{int64(1), int64(9)}, {int64(2), int64(20)}, {int64(3), int64(30)}}},
		{name: "full", q: "UPDATE t x SET v=r.z FROM s FULL JOIN r USING(k) WHERE x.id=k", count: 3, rows: [][]any{{int64(1), int64(9)}, {int64(2), nil}, {int64(3), int64(30)}}},
		{name: "scalar_correlation", q: "UPDATE t x SET v=(SELECT s.n FROM s WHERE s.k=x.id)", count: 3, rows: [][]any{{int64(1), int64(101)}, {int64(2), int64(202)}, {int64(3), nil}}},
		{name: "source_lateral", q: "UPDATE t x SET v=q.n FROM s CROSS JOIN LATERAL (SELECT s.n) q WHERE x.id=s.k", count: 2, rows: [][]any{{int64(1), int64(101)}, {int64(2), int64(202)}, {int64(3), int64(30)}}},
		{name: "target_lateral", q: "UPDATE t x SET v=1 FROM LATERAL (SELECT x.id) q", code: "42P10", message: "invalid reference to FROM-clause entry for table \"x\"", token: "x.id", hint: "There is an entry for table \"x\", but it cannot be referenced from this part of the query."},
		{name: "target_derived", q: "UPDATE t x SET v=1 FROM (SELECT x.id) q", code: "42P01", message: "invalid reference to FROM-clause entry for table \"x\"", token: "x.id", detail: "There is an entry for table \"x\", but it cannot be referenced from this part of the query."},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
					run(referenceExec("CREATE TABLE s(k INT,n INT)", 0))
					run(referenceExec("INSERT INTO s VALUES(1,101),(2,202)", 2))
					run(referenceExec("CREATE TABLE r(k INT,z INT)", 0))
					run(referenceExec("INSERT INTO r VALUES(1,9)", 1))
					if tc.name == "full" {
						run(referenceExec("INSERT INTO r VALUES(3,30)", 1))
					}
					expected := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}
					shared := tc.name == "left" || tc.name == "right" || tc.name == "full" || tc.name == "scalar_correlation" || tc.name == "target_derived"
					if b.pg || shared {
						if tc.code != "" {
							want := controlsReferenceDiagnostic(tc.code, tc.message)
							want.Position = updateFromPosition(tc.q, tc.token)
							want.Detail = tc.detail
							want.Hint = tc.hint
							updateFromDiagnostic(t, p.ctx, c, tc.q, want, prepared)
						} else {
							step := referenceExec(tc.q, tc.count)
							if prepared {
								step = referencePrepared(step)
							}
							run(step)
							expected = tc.rows
						}
					} else {
						// Intentional native capability boundary, not successful no-op/parity.
						var err error
						if prepared {
							var stmt *sql.Stmt
							stmt, err = c.PrepareContext(p.ctx, tc.q)
							if stmt != nil {
								stmt.Close()
							}
						} else {
							_, err = c.ExecContext(p.ctx, tc.q)
						}
						if err == nil {
							t.Fatal("excluded LATERAL mechanism succeeded")
						}
					}
					run(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, expected))
				})
			})
		}
	}
}
