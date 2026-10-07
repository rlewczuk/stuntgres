//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRandomCountStageEmptyAndGroups(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE counts(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO counts VALUES(1),(2),(2)", 3))
	p.step(t, referenceExec("CREATE TABLE no_counts(id INT)", 0))
	for _, s := range []referenceStep{
		referenceSelect("SELECT random(count(*),count(*)) FROM counts", []string{"random"}, []string{"INT8"}, [][]any{{int64(3)}}),
		referenceSelect("SELECT random(count(*),count(*)) FROM no_counts", []string{"random"}, []string{"INT8"}, [][]any{{int64(0)}}),
		referenceSelect("SELECT id,random(count(*),count(*)) FROM counts GROUP BY id ORDER BY id", []string{"id", "random"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}),
	} {
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
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
			first := draw()
			for _, q := range []string{"SELECT random(count(*),count(*)) FROM counts", "SELECT random(count(*),count(*)) FROM no_counts", "SELECT id,random(count(*),count(*)) FROM counts GROUP BY id ORDER BY id"} {
				for _, prepared := range []bool{false, true} {
					seed()
					if prepared {
						s, e := c.PrepareContext(p.ctx, q)
						if e != nil {
							t.Fatal(e)
						}
						if _, e = s.ExecContext(p.ctx); e != nil {
							t.Fatal(e)
						}
						s.Close()
					} else if _, e = c.ExecContext(p.ctx, q); e != nil {
						t.Fatal(e)
					}
					if draw() != first {
						t.Fatal("equal aggregate bounds consumed draw", q)
					}
				}
			}
		})
	}
}
