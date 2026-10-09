package reference_test

import (
	"database/sql"
	"math"
	"testing"
)

func TestReferenceRandomSessionLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			one := func(c *sql.Conn, q string) any {
				name, typ := "random", "FLOAT8"
				if q == "SELECT random_normal()" {
					name = "random_normal"
				}
				return randomScalar(t, p, c, backend, q, name, typ)
			}
			active := false
			defer func() {
				if active {
					if _, err := a.ExecContext(p.ctx, "ROLLBACK"); err != nil {
						t.Errorf("transaction cleanup: %v", err)
					}
				}
			}()
			exec := func(c *sql.Conn, q string) {
				referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec(q, 0))
				switch q {
				case "BEGIN", "BEGIN READ ONLY":
					active = true
				case "COMMIT", "ROLLBACK":
					active = false
				}
			}
			seed := func(c *sql.Conn) { randomReseed(t, p, c, backend, .25) }
			seed(a)
			first := one(a, "SELECT random()")
			second := one(a, "SELECT random()")
			seed(a)
			exec(a, "BEGIN")
			exec(a, "SAVEPOINT mark")
			one(a, "SELECT random()")
			exec(a, "ROLLBACK TO SAVEPOINT mark")
			if one(a, "SELECT random()") != second {
				t.Fatal("savepoint undid draw")
			}
			exec(a, "ROLLBACK")
			exec(a, "BEGIN")
			exec(a, "SAVEPOINT mark")
			seed(a)
			exec(a, "ROLLBACK TO SAVEPOINT mark")
			if one(a, "SELECT random()") != first {
				t.Fatal("savepoint undid seed")
			}
			exec(a, "ROLLBACK")
			exec(a, "BEGIN")
			seed(a)
			exec(a, "ROLLBACK")
			if one(a, "SELECT random()") != first {
				t.Fatal("rollback undid seed")
			}
			exec(a, "BEGIN READ ONLY")
			seed(a)
			one(a, "SELECT random()")
			one(a, "SELECT random_normal()")
			exec(a, "COMMIT")
		})
	}
}
func TestReferenceRandomSessionIsolationAndRollback(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, e := b.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			peer, e := b.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer peer.Close()
			draw := func(c referenceExecutor) any { return randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8") }
			randomReseed(t, p, a, b, .25)
			first := draw(a)
			second := draw(a)
			randomReseed(t, p, a, b, .25)
			randomReseed(t, p, peer, b, .25)
			if draw(peer) != first {
				t.Fatal("peer first")
			}
			draw(a)
			randomReseed(t, p, a, b, -1)
			if draw(peer) != second {
				t.Fatal("cross session seed")
			}
			randomReseed(t, p, a, b, .25)
			tx, e := a.BeginTx(p.ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			var transactionDraw float64
			values := randomRead(t, p.ctx, tx, b, "SELECT random()", []string{"random"}, []string{"FLOAT8"}, func(t *testing.T, rows *sql.Rows, rowIndex int) {
				if err := rows.Scan(&transactionDraw); err != nil {
					t.Fatal("transaction float64 scan", err)
				}
				if rowIndex != 0 {
					t.Fatal("transaction extra draw", rowIndex)
				}
			})
			if len(values) != 1 || values[0][0] != transactionDraw {
				t.Fatal("transaction typed/raw mismatch", values, transactionDraw)
			}
			if math.IsNaN(transactionDraw) || transactionDraw < 0 || transactionDraw >= 1 {
				t.Fatal("transaction draw range", transactionDraw)
			}
			if transactionDraw != first {
				t.Fatal("transaction first", transactionDraw, first)
			}
			if e := tx.Rollback(); e != nil {
				t.Fatal(e)
			}
			if draw(a) != second {
				t.Fatal("rollback rewound draw")
			}
		})
	}
}
func TestReferenceRandomPoolPhysicalLifetime(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			b.db.SetMaxIdleConns(0)
			b.db.SetMaxOpenConns(1)
			b.db.SetMaxIdleConns(1)
			c, e := b.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			identity := func(c *sql.Conn) any {
				if b.pg {
					var pid int64
					if e := c.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); e != nil {
						t.Fatal(e)
					}
					return pid
				}
				var id any
				if e := c.Raw(func(raw any) error { id = raw; return nil }); e != nil {
					t.Fatal(e)
				}
				return id
			}
			before := identity(c)
			randomReseed(t, p, c, b, .25)
			first := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			second := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			randomReseed(t, p, c, b, .25)
			if randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8") != first {
				t.Fatal("baseline")
			}
			if e := c.Close(); e != nil {
				t.Fatal(e)
			}
			next, e := b.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			if identity(next) != before || randomScalar(t, p, next, b, "SELECT random()", "random", "FLOAT8") != second {
				t.Fatal("physical reuse")
			}
			referenceRunStep(t, p.ctx, next, b.recorder, referenceExec("CREATE TABLE sentinel(id INT)", 0))
			referenceRunStep(t, p.ctx, next, b.recorder, referenceExec("INSERT INTO sentinel VALUES(7)", 1))
			b.db.SetMaxIdleConns(0)
			if e := next.Close(); e != nil {
				t.Fatal(e)
			}
			replacement, e := b.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer replacement.Close()
			if identity(replacement) == before {
				t.Fatal("physical replacement")
			}
			randomReseed(t, p, replacement, b, .25)
			if randomScalar(t, p, replacement, b, "SELECT random()", "random", "FLOAT8") != first {
				t.Fatal("replacement seed")
			}
			referenceRunStep(t, p.ctx, replacement, b.recorder, referenceSelect("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
