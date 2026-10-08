package stuntgres_test

import (
	"context"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestDeparseConstraintsReviewRandomStrictConsumers(t *testing.T) {
	for _, tc := range []struct{ name, decl, input, compact, pretty, control, good, bad, errorCode, errorMessage string }{
		{"int", "INT", "random(a,b) IS NOT NULL", "(random(a, b) IS NOT NULL)", "random(a, b) IS NOT NULL", "random(7,7)", "7,7", "2,1", "22023", "lower bound must be less than or equal to upper bound"},
		{"bigint", "BIGINT", "random(a,b) IS NOT NULL", "(random(a, b) IS NOT NULL)", "random(a, b) IS NOT NULL", "random(7::bigint,7::bigint)", "7,7", "2,1", "22023", "lower bound must be less than or equal to upper bound"},
		{"numeric", "NUMERIC", "random(a,b) IS NOT NULL", "(random(a, b) IS NOT NULL)", "random(a, b) IS NOT NULL", "random(7::numeric,7::numeric)", "7,7", "2,1", "22023", "lower bound must be less than or equal to upper bound"},
		{"normal_null", "DOUBLE PRECISION", "random_normal(a,0) IS NOT NULL", "(random_normal(a, (0)::double precision) IS NOT NULL)", "random_normal(a, 0::double precision) IS NOT NULL", "random_normal(5,0)", "5,0", "", "", ""},
		{"seed", "DOUBLE PRECISION", "setseed(a) IS NOT NULL", "(setseed(a) IS NOT NULL)", "setseed(a) IS NOT NULL", "setseed(0.25)", "0.25,0", "2,0", "22023", "setseed parameter 2 is out of allowed range [-1,1]"},
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
			seed()
			first := draw()
			seed()
			var control any
			if err := conn.QueryRowContext(context.Background(), "SELECT "+tc.control).Scan(&control); err != nil {
				t.Fatal(err)
			}
			after := draw()
			if r, e := conn.ExecContext(context.Background(), "CREATE TABLE original(id INT,a "+tc.decl+",b "+tc.decl+",CONSTRAINT c CHECK("+tc.input+"))"); e != nil {
				t.Fatal(e)
			} else if n, _ := r.RowsAffected(); n != 0 {
				t.Fatal(n)
			}
			seed()
			for mode, expr := range []string{tc.compact, tc.pretty} {
				q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
				deparseReviewConnText(t, conn, q, "CHECK ("+expr+")")
				var actual string
				if err := conn.QueryRowContext(context.Background(), q).Scan(&actual); err != nil {
					t.Fatal(err)
				}
				if actual != "CHECK ("+expr+")" {
					t.Fatal(actual, expr)
				}
				if r, e := conn.ExecContext(context.Background(), fmt.Sprintf("CREATE TABLE copy%d(id INT,a %s,b %s,CONSTRAINT c%d %s)", mode, tc.decl, tc.decl, mode, actual)); e != nil {
					t.Fatal(e)
				} else if n, _ := r.RowsAffected(); n != 0 {
					t.Fatal(n)
				}
			}
			if got := draw(); got != first {
				t.Fatal("deparse/CREATE changed seed", got, first)
			}
			for i, name := range []string{"original", "copy0", "copy1"} {
				constraint := "c"
				if i > 0 {
					constraint = fmt.Sprintf("c%d", i-1)
				}
				for _, prepared := range []bool{false, true} {
					seed()
					query := "INSERT INTO " + name + " VALUES(1," + tc.good + ")"
					if prepared {
						s, e := conn.PrepareContext(context.Background(), query)
						if e != nil {
							t.Fatal(e)
						}
						_, e = s.ExecContext(context.Background())
						s.Close()
						if e != nil {
							t.Fatal(e)
						}
					} else if _, err := conn.ExecContext(context.Background(), query); err != nil {
						t.Fatal(err)
					}
					if got := draw(); got != after {
						t.Fatal("provider effect", got, after)
					}
					seed()
					query = "INSERT INTO " + name + " VALUES(2,NULL,0)"
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
					detail := "2, null, 0"
					notNullDiagnostic(t, err, checkViolation(name, constraint, detail))
					if got := draw(); got != first {
						t.Fatal("strict NULL consumed/reseeded", got, first)
					}
					{
						var count int
						if e := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+name).Scan(&count); e != nil || count != 1 {
							t.Fatal(count, e)
						}
						var id int
						var same bool
						parts := strings.Split(tc.good, ",")
						if e := conn.QueryRowContext(context.Background(), "SELECT id,a="+parts[0]+" AND b="+parts[1]+" FROM "+name).Scan(&id, &same); e != nil || id != 1 || !same {
							t.Fatal(id, same, e)
						}
					}
					if tc.bad != "" {
						for _, query := range []string{"INSERT INTO " + name + " VALUES(2," + tc.bad + ")", "UPDATE " + name + " SET a=" + strings.Split(tc.bad, ",")[0] + ",b=" + strings.Split(tc.bad, ",")[1] + " WHERE id=1"} {
							seed()
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
							notNullDiagnostic(t, err, stuntgres.Error{Code: tc.errorCode, Message: tc.errorMessage})
							if got := draw(); got != first {
								t.Fatal("invalid provider changed stream", got, first)
							}
							{
								var count int
								if e := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+name).Scan(&count); e != nil || count != 1 {
									t.Fatal(count, e)
								}
								var id int
								var same bool
								parts := strings.Split(tc.good, ",")
								if e := conn.QueryRowContext(context.Background(), "SELECT id,a="+parts[0]+" AND b="+parts[1]+" FROM "+name).Scan(&id, &same); e != nil || id != 1 || !same {
									t.Fatal(id, same, e)
								}
							}
						}
						// First accepted provider call remains visible after batch publication fails.
						seed()
						var ignored any
						if err := conn.QueryRowContext(context.Background(), "SELECT "+tc.control).Scan(&ignored); err != nil {
							t.Fatal(err)
						}
						want := draw()
						seed()
						query = "INSERT INTO " + name + " VALUES(3," + tc.good + "),(4," + tc.bad + "),(5," + tc.good + ")"
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
						notNullDiagnostic(t, err, stuntgres.Error{Code: tc.errorCode, Message: tc.errorMessage})
						if got := draw(); got != want {
							t.Fatal("failed batch provider frontier", got, want)
						}
						{
							var count int
							if e := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+name).Scan(&count); e != nil || count != 1 {
								t.Fatal(count, e)
							}
							var id int
							var same bool
							parts := strings.Split(tc.good, ",")
							if e := conn.QueryRowContext(context.Background(), "SELECT id,a="+parts[0]+" AND b="+parts[1]+" FROM "+name).Scan(&id, &same); e != nil || id != 1 || !same {
								t.Fatal(id, same, e)
							}
						}
					}
					if r, e := conn.ExecContext(context.Background(), "DELETE FROM "+name); e != nil {
						t.Fatal(e)
					} else if n, _ := r.RowsAffected(); n != 1 {
						t.Fatal(n)
					}
				}
			}

		})
	}
}
