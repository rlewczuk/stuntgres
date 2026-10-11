package reference_test

import (
	"database/sql"
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRandomSessionLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, e := backend.DB.Conn(p.Ctx)
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
					if _, err := a.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
						t.Errorf("transaction cleanup: %v", err)
					}
				}
			}()
			exec := func(c *sql.Conn, q string) {
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec(q, 0))
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
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			peer, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer peer.Close()
			draw := func(c fixture.Executor) any { return randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8") }
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
			tx, e := a.BeginTx(p.Ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			var transactionDraw float64
			values := randomRead(t, p.Ctx, tx, b, "SELECT random()", []string{"random"}, []string{"FLOAT8"}, func(t *testing.T, rows *sql.Rows, rowIndex int) {
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
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			b.DB.SetMaxIdleConns(0)
			b.DB.SetMaxOpenConns(1)
			b.DB.SetMaxIdleConns(1)
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			identity := func(c *sql.Conn) any {
				if b.PG {
					var pid int64
					if e := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); e != nil {
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
			next, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			if identity(next) != before || randomScalar(t, p, next, b, "SELECT random()", "random", "FLOAT8") != second {
				t.Fatal("physical reuse")
			}
			fixture.RunStep(t, p.Ctx, next, b.Recorder, fixture.Exec("CREATE TABLE sentinel(id INT)", 0))
			fixture.RunStep(t, p.Ctx, next, b.Recorder, fixture.Exec("INSERT INTO sentinel VALUES(7)", 1))
			b.DB.SetMaxIdleConns(0)
			if e := next.Close(); e != nil {
				t.Fatal(e)
			}
			replacement, e := b.DB.Conn(p.Ctx)
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
			fixture.RunStep(t, p.Ctx, replacement, b.Recorder, fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
