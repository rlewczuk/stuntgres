package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

func TestReferenceAlterRenamePrepared(t *testing.T) {
	for _, tablePrepared := range []bool{false, true} {
		t.Run(fmt.Sprint(tablePrepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					c, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					record := backend.recorder
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, record, s) }
					run(referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
					run(referenceExec("INSERT INTO t VALUES(7,'keep')", 1))
					statements := []*sql.Stmt{}
					defer func() {
						for _, s := range statements {
							if err := s.Close(); err != nil {
								t.Error(err)
							}
						}
					}()
					prepare := func(q string) *sql.Stmt {
						s, e := c.PrepareContext(p.ctx, q)
						if e != nil {
							t.Fatal(q, e)
						}
						statements = append(statements, s)
						return s
					}
					star, named, other := prepare("SELECT * FROM t"), prepare("SELECT a FROM t"), prepare("SELECT b FROM t")
					target, ordered := prepare("INSERT INTO t(a,b) VALUES($1,$2)"), prepare("INSERT INTO t VALUES($1,$2)")
					if !backend.pg {
						held, e := p.native.QueryContext(p.ctx, "SELECT * FROM t")
						if e != nil {
							t.Fatal(e)
						}
						defer held.Close()
						run(referencePrepared(referenceExec("ALTER TABLE t RENAME a TO renamed", 0)))
						labels, e := held.Columns()
						if e != nil || !reflect.DeepEqual(labels, []string{"a", "b"}) {
							t.Fatal(labels, e)
						}
						if !held.Next() {
							t.Fatal(held.Err())
						}
						var a int64
						var b string
						if e = held.Scan(&a, &b); e != nil || a != 7 || b != "keep" {
							t.Fatal(a, b, e)
						}
						if held.Next() || held.Err() != nil {
							t.Fatal("retained original row exhaustion", held.Err())
						}
						if e = held.Close(); e != nil {
							t.Fatal(e)
						}
					} else {
						run(referencePrepared(referenceExec("ALTER TABLE t RENAME a TO renamed", 0)))
					}
					for _, tc := range []struct {
						s                          *sql.Stmt
						q, code, message, position string
					}{
						{star, "SELECT * FROM t", "0A000", "cached plan must not change result type", ""},
						{named, "SELECT a FROM t", "42703", `column "a" does not exist`, "8"},
					} {
						failure := referenceDDLPositionError(tc.q, tc.code, tc.message, tc.position, false, "execute")
						failure.selectRows = true
						referenceRunStep(t, p.ctx, referenceHeldStatement{c, tc.s, tc.q}, record, failure)
					}
					failure := referenceDDLPositionError("INSERT INTO t(a,b) VALUES($1,$2)", "42703", `column "a" of relation "t" does not exist`, "15", false, "execute")
					failure.args = []any{8, "bad"}
					referenceRunStep(t, p.ctx, referenceHeldStatement{c, target, failure.query}, record, failure)
					referenceRunStep(t, p.ctx, referenceHeldStatement{c, other, "SELECT b FROM t"}, record, referenceSelect("SELECT b FROM t", []string{"b"}, []string{"TEXT"}, [][]any{{"keep"}}))
					referenceRunStep(t, p.ctx, referenceHeldStatement{c, ordered, "INSERT INTO t VALUES($1,$2)"}, record, referenceExec("INSERT INTO t VALUES($1,$2)", 1, 8, "tail"))
					run(referencePrepared(referenceExec("INSERT INTO t(renamed,b) VALUES($1,$2)", 1, 9, "fresh")))
					expected := [][]any{{int64(7), "keep"}, {int64(8), "tail"}, {int64(9), "fresh"}}
					run(referencePrepared(referenceSelect("SELECT * FROM t ORDER BY renamed", []string{"renamed", "b"}, []string{"INT4", "TEXT"}, expected)))
					rename := referenceExec("ALTER TABLE t RENAME TO newtable", 0)
					rename.prepared = tablePrepared
					run(rename)
					failure = referenceDDLPositionError("SELECT b FROM t", "42P01", `relation "t" does not exist`, "15", false, "execute")
					failure.selectRows = true
					referenceRunStep(t, p.ctx, referenceHeldStatement{c, other, failure.query}, record, failure)
					late := prepare("ALTER TABLE late RENAME TO later")
					run(referenceExec("CREATE TABLE late(a INT)", 0))
					referenceRunStep(t, p.ctx, referenceHeldStatement{c, late, "ALTER TABLE late RENAME TO later"}, record, referenceExec("ALTER TABLE late RENAME TO later", 0))
					observed := referenceRunStep(t, p.ctx, c, record, referenceSelect("SELECT * FROM newtable ORDER BY renamed", []string{"renamed", "b"}, []string{"INT4", "TEXT"}, expected))
					if !reflect.DeepEqual(observed.columns, []string{"renamed", "b"}) {
						t.Fatal(observed)
					}
				})
			}
		})
	}
}
