package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeRandomSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			a, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			one := func(c fixture.Executor, q, name, typ string, args ...any) any {
				return randomScalar(t, p, c, backend, q, name, typ, args...)
			}
			seed := func(c fixture.Executor) { randomReseed(t, p, c, backend, 0.25) }
			for _, tc := range []struct {
				q, name, typ string
				want         any
			}{
				{"SELECT random()", "random", "FLOAT8", nil},
				{"SELECT random(7,7)", "random", "INT4", int64(7)},
				{"SELECT random(2147483648,2147483648)", "random", "INT8", int64(2147483648)},
				{"SELECT random(1.0,1.000)", "random", "NUMERIC", "1.000"},
				{"SELECT random_normal(5,0)", "random_normal", "FLOAT8", float64(5)},
				{"SELECT setseed(0)", "setseed", "VOID", ""},
				{"SELECT setseed(NULL)", "setseed", "VOID", nil},
			} {
				func() {
					stmt, err := a.PrepareContext(p.Ctx, tc.q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					// Preserve the source's actual non-Context statement Query route.
					if tc.q == "SELECT random()" {
						rows, err := stmt.Query()
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						names, err := rows.Columns()
						if err != nil || len(names) != 1 || names[0] != tc.name {
							t.Fatal(names, err)
						}
						meta, err := rows.ColumnTypes()
						if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != tc.typ {
							t.Fatal(meta, err)
						}
						if !rows.Next() {
							t.Fatal(rows.Err())
						}
						var v any
						if err := rows.Scan(&v); err != nil {
							t.Fatal(err)
						}
						x, ok := v.(float64)
						if !ok || !(x >= 0 && x < 1) {
							t.Fatal(v)
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal(rows.Err())
						}
					} else {
						held := fixture.HeldStatement{db, stmt, tc.q}
						step := fixture.Select(tc.q, []string{tc.name}, []string{tc.typ}, [][]any{{tc.want}})
						step.NonContext = true
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, step)
					}
				}()
			}
			seed(a)
			first := one(a, "SELECT random()", "random", "FLOAT8")
			second := one(a, "SELECT random()", "random", "FLOAT8")
			seed(a)
			seed(b)
			if one(a, "SELECT random()", "random", "FLOAT8") != first || one(b, "SELECT random()", "random", "FLOAT8") != first {
				t.Fatal("seed repeat/isolation")
			}
			seed(a)
			if one(b, "SELECT random()", "random", "FLOAT8") != second {
				t.Fatal("seed leaked")
			}
			seed(a)
			implicit := one(a, "SELECT random_normal()", "random_normal", "FLOAT8")
			seed(a)
			if one(a, "SELECT random_normal(0,1)", "random_normal", "FLOAT8") != implicit {
				t.Fatal("defaults")
			}
			seed(a)
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var d float64
			if err := tx.QueryRow("SELECT random()").Scan(&d); err != nil {
				t.Fatal(err)
			}
			if d != first {
				t.Fatal("transaction first draw", d, first)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if one(a, "SELECT random()", "random", "FLOAT8") != second {
				t.Fatal("rollback rewound PRNG")
			}
			failure := fixture.ImplicitError("SELECT setseed(1.23456789)", fixture.Diagnostic{Code: "22023", Message: "setseed parameter 1.23457 is out of allowed range [-1,1]"})
			failure.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, failure)
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Exec("SELECT setseed(0)", 1))
			for range 8 {
				if n := one(a, "SELECT random(-7,3)", "random", "INT4").(int64); n < -7 || n > 3 {
					t.Fatal(n)
				}
				if n := one(a, "SELECT random(2147483648,2147483650)", "random", "INT8").(int64); n < 2147483648 || n > 2147483650 {
					t.Fatal(n)
				}
			}
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Select("SELECT random_normal(NULL,1)", []string{"random_normal"}, []string{"FLOAT8"}, [][]any{{nil}}))
			if backend.PG {
				return
			} // Raw native physical identity and pooled PRNG retention.
			db.SetMaxIdleConns(0)
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(1)
			identity := func(c *sql.Conn) any {
				var id any
				if err := c.Raw(func(raw any) error { id = raw; return nil }); err != nil {
					t.Fatal(err)
				}
				return id
			}
			before := identity(a)
			seed(a)
			one(a, "SELECT random()", "random", "FLOAT8")
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			reused, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reused.Close()
			if identity(reused) != before {
				t.Fatal("pool replaced seeded physical connection")
			}
			if one(reused, "SELECT random()", "random", "FLOAT8") != second {
				t.Fatal("pool return reset seeded stream")
			}
		})
	}
}
