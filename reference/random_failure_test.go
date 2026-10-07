//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRandomFailedInsertRetainsDraw(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE random_fail_source(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO random_fail_source VALUES(1)", 1))
	p.step(t, referenceExec("CREATE TABLE random_fail_sink(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO random_fail_sink VALUES(99)", 1))
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"postgres", p.postgres}, {"native", p.memory}} {
		t.Run(backend.name, func(t *testing.T) {
			c, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			seed := func() {
				t.Helper()
				if _, e := c.ExecContext(p.ctx, "SELECT setseed(0.25)"); e != nil {
					t.Fatal(e)
				}
			}
			draw := func() float64 {
				t.Helper()
				var n float64
				if e := c.QueryRowContext(p.ctx, "SELECT random()").Scan(&n); e != nil {
					t.Fatal(e)
				}
				return n
			}
			seed()
			var inner int64
			if e := c.QueryRowContext(p.ctx, "SELECT random(2,3)").Scan(&inner); e != nil || inner < 2 || inner > 3 {
				t.Fatal("independent inner range", inner, e)
			}
			expected := draw()
			// Exactly one source row and a nested argument dependency force the
			// inner draw before the outer reversed-bound error, without relying
			// on row/projection order or PostgreSQL planner implementation.
			q := "INSERT INTO random_fail_sink SELECT random(random(2,3),1) FROM random_fail_source"
			s, e := c.PrepareContext(p.ctx, q)
			if e != nil {
				t.Fatal("prepare invoked provider", e)
			}
			defer s.Close()
			for _, prepared := range []bool{false, true} {
				seed()
				var e error
				if prepared {
					_, e = s.ExecContext(p.ctx)
				} else {
					_, e = c.ExecContext(p.ctx, q)
				}
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "lower bound must be less than or equal to upper bound"}
				referenceAssertDiagnostic(t, e, &want)
				if draw() != expected {
					t.Fatal("failed INSERT rewound inner draw")
				}
				rows, e := c.QueryContext(p.ctx, "SELECT id FROM random_fail_sink ORDER BY id")
				if e != nil {
					t.Fatal(e)
				}
				if !rows.Next() {
					t.Fatal("sentinel missing", rows.Err())
				}
				var id int64
				if e = rows.Scan(&id); e != nil || id != 99 {
					t.Fatal("sentinel changed", id, e)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("failed INSERT published a row", rows.Err())
				}
				rows.Close()
			}
		})
	}
}
