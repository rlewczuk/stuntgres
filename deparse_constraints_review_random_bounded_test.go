package stuntgres_test

import (
	"context"
	"fmt"
	"testing"
)

func TestDeparseConstraintsReviewRandomBoundedConsumers(t *testing.T) {
	for _, tc := range []struct{ name, decl, call, compact, pretty string }{
		{"int", "INT", "random(7,7)", "random(7, 7)", "random(7, 7)"},
		{"bigint", "BIGINT", "random(7::bigint,7::bigint)", "random((7)::bigint, (7)::bigint)", "random(7::bigint, 7::bigint)"},
		{"numeric", "NUMERIC", "random(7::numeric,7::numeric)", "random((7)::numeric, (7)::numeric)", "random(7::numeric, 7::numeric)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, conn := randomConnection(t)
			_ = db
			seed := func() {
				var ignored any
				if err := conn.QueryRowContext(context.Background(), "SELECT setseed(0.25)").Scan(&ignored); err != nil {
					t.Fatal(err)
				}
			}
			draw := func() float64 {
				var v float64
				if err := conn.QueryRowContext(context.Background(), "SELECT random()").Scan(&v); err != nil {
					t.Fatal(err)
				}
				return v
			}
			control := func() {
				var value string
				if err := conn.QueryRowContext(context.Background(), "SELECT "+tc.call).Scan(&value); err != nil || value != "7" {
					t.Fatal(value, err)
				}
			}
			seed()
			control()
			after := draw()
			seed()
			first := draw()
			seed()
			if r, e := conn.ExecContext(context.Background(), "CREATE TABLE original(id INT,a "+tc.decl+",CONSTRAINT c CHECK("+tc.call+">a))"); e != nil {
				t.Fatal(e)
			} else if n, _ := r.RowsAffected(); n != 0 {
				t.Fatal(n)
			}
			for mode, call := range []string{tc.compact, tc.pretty} {
				expr := "(" + call + " > a)"
				if mode == 1 {
					expr = call + " > a"
				}
				q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
				deparseReviewConnText(t, conn, q, "CHECK ("+expr+")")
				var actual string
				if err := conn.QueryRowContext(context.Background(), q).Scan(&actual); err != nil {
					t.Fatal(err)
				}
				if actual != "CHECK ("+expr+")" {
					t.Fatal(actual, expr)
				}
				if r, e := conn.ExecContext(context.Background(), fmt.Sprintf("CREATE TABLE copy%d(id INT,a %s,CONSTRAINT c%d %s)", mode, tc.decl, mode, actual)); e != nil {
					t.Fatal(e)
				} else if n, _ := r.RowsAffected(); n != 0 {
					t.Fatal(n)
				}
			}
			if got := draw(); got != first {
				t.Fatal("construction effect", got, first)
			}
			for i, name := range []string{"original", "copy0", "copy1"} {
				constraint := "c"
				if i > 0 {
					constraint = fmt.Sprintf("c%d", i-1)
				}
				for _, prepared := range []bool{false, true} {
					for _, item := range []struct {
						id     int
						value  string
						reject bool
					}{{1, "6", false}, {2, "7", true}, {3, "NULL", false}} {
						seed()
						query := fmt.Sprintf("INSERT INTO %s VALUES(%d,%s)", name, item.id, item.value)
						var err error
						if prepared {
							s, e := conn.PrepareContext(context.Background(), query)
							if e != nil {
								t.Fatal(e)
							}
							_, err = s.ExecContext(context.Background())
							s.Close()
						} else {
							_, err = conn.ExecContext(context.Background(), query)
						}
						if item.reject {
							detail := "2, 7"
							notNullDiagnostic(t, err, checkViolation(name, constraint, detail))
						} else if err != nil {
							t.Fatal(err)
						}
						if got := draw(); got != after {
							t.Fatal("bounded call effect", got, after)
						}
					}
					{
						rows, e := conn.QueryContext(context.Background(), "SELECT id,a FROM "+name+" ORDER BY id")
						if e != nil {
							t.Fatal(e)
						}
						if !rows.Next() {
							t.Fatal(rows.Err())
						}
						var id int
						var a string
						if e := rows.Scan(&id, &a); e != nil || id != 1 || a != "6" {
							t.Fatal(id, a, e)
						}
						if !rows.Next() {
							t.Fatal(rows.Err())
						}
						var null any
						if e := rows.Scan(&id, &null); e != nil || id != 3 || null != nil {
							t.Fatal(id, null, e)
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal(rows.Err())
						}
						rows.Close()
					}
					seed()
					control()
					control()
					afterBatch := draw()
					seed()
					query := "INSERT INTO " + name + " VALUES(4,6),(5,7),(6,6)"
					var err error
					if prepared {
						s, e := conn.PrepareContext(context.Background(), query)
						if e != nil {
							t.Fatal(e)
						}
						_, err = s.ExecContext(context.Background())
						s.Close()
					} else {
						_, err = conn.ExecContext(context.Background(), query)
					}
					detail := "5, 7"
					notNullDiagnostic(t, err, checkViolation(name, constraint, detail))
					if got := draw(); got != afterBatch {
						t.Fatal("failed batch calls", got, afterBatch)
					}
					{
						rows, e := conn.QueryContext(context.Background(), "SELECT id,a FROM "+name+" ORDER BY id")
						if e != nil {
							t.Fatal(e)
						}
						if !rows.Next() {
							t.Fatal(rows.Err())
						}
						var id int
						var a string
						if e := rows.Scan(&id, &a); e != nil || id != 1 || a != "6" {
							t.Fatal(id, a, e)
						}
						if !rows.Next() {
							t.Fatal(rows.Err())
						}
						var null any
						if e := rows.Scan(&id, &null); e != nil || id != 3 || null != nil {
							t.Fatal(id, null, e)
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal(rows.Err())
						}
						rows.Close()
					}
					seed()
					query = "UPDATE " + name + " SET a=7 WHERE id=1"
					if prepared {
						s, e := conn.PrepareContext(context.Background(), query)
						if e != nil {
							t.Fatal(e)
						}
						_, err = s.ExecContext(context.Background())
						s.Close()
					} else {
						_, err = conn.ExecContext(context.Background(), query)
					}
					detail = "1, 7"
					notNullDiagnostic(t, err, checkViolation(name, constraint, detail))
					if got := draw(); got != after {
						t.Fatal("failed update call", got, after)
					}
					{
						rows, e := conn.QueryContext(context.Background(), "SELECT id,a FROM "+name+" ORDER BY id")
						if e != nil {
							t.Fatal(e)
						}
						if !rows.Next() {
							t.Fatal(rows.Err())
						}
						var id int
						var a string
						if e := rows.Scan(&id, &a); e != nil || id != 1 || a != "6" {
							t.Fatal(id, a, e)
						}
						if !rows.Next() {
							t.Fatal(rows.Err())
						}
						var null any
						if e := rows.Scan(&id, &null); e != nil || id != 3 || null != nil {
							t.Fatal(id, null, e)
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal(rows.Err())
						}
						rows.Close()
					}
					if r, e := conn.ExecContext(context.Background(), "DELETE FROM "+name); e != nil {
						t.Fatal(e)
					} else if n, _ := r.RowsAffected(); n != 2 {
						t.Fatal(n)
					}
				}
			}

		})
	}
}
