package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeparseConstraintsReviewRandomStrictConsumers(t *testing.T) {
	for _, tc := range []struct{ name, decl, input, compact, pretty, control, good, bad, errorCode, errorMessage string }{
		{"int", "INT", "random(a,b) IS NOT NULL", "(random(a, b) IS NOT NULL)", "random(a, b) IS NOT NULL", "random(7,7)", "7,7", "2,1", "22023", "lower bound must be less than or equal to upper bound"},
		{"bigint", "BIGINT", "random(a,b) IS NOT NULL", "(random(a, b) IS NOT NULL)", "random(a, b) IS NOT NULL", "random(7::bigint,7::bigint)", "7,7", "2,1", "22023", "lower bound must be less than or equal to upper bound"},
		{"numeric", "NUMERIC", "random(a,b) IS NOT NULL", "(random(a, b) IS NOT NULL)", "random(a, b) IS NOT NULL", "random(7::numeric,7::numeric)", "7,7", "2,1", "22023", "lower bound must be less than or equal to upper bound"},
		{"normal_null", "DOUBLE PRECISION", "random_normal(a,0) IS NOT NULL", "(random_normal(a, (0)::double precision) IS NOT NULL)", "random_normal(a, 0::double precision) IS NOT NULL", "random_normal(5,0)", "5,0", "", "", ""},
		{"seed", "DOUBLE PRECISION", "setseed(a) IS NOT NULL", "(setseed(a) IS NOT NULL)", "setseed(a) IS NOT NULL", "setseed(0.25)", "0.25,0", "2,0", "22023", "setseed parameter 2 is out of allowed range [-1,1]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.DeparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, _ bool, run func(fixture.Step) fixture.Observed) {
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
				run(fixture.Exec("CREATE TABLE original(id INT,a "+tc.decl+",b "+tc.decl+",CONSTRAINT c CHECK("+tc.input+"))", 0))
				seed()
				for mode, expr := range []string{tc.compact, tc.pretty} {
					q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
					step := fixture.Select(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
					actual := run(step).Rows[0][0].(string)
					run(fixture.Prepared(step))
					run(fixture.Exec(fmt.Sprintf("CREATE TABLE copy%d(id INT,a %s,b %s,CONSTRAINT c%d %s)", mode, tc.decl, tc.decl, mode, actual), 0))
				}
				if got := draw(); got != first {
					t.Fatal("deparse/CREATE changed seed", got, first)
				}
				deparseStrictConsumers(t, conn, tc, first, after, seed, draw, run)
			})
		})
	}
}

func deparseStrictConsumers(t *testing.T, conn *sql.Conn, tc struct{ name, decl, input, compact, pretty, control, good, bad, errorCode, errorMessage string }, first, after float64, seed func(), draw func() float64, run func(fixture.Step) fixture.Observed) {
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
			{
				want := fixture.CheckViolation(name, constraint, detail)
				want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
				fixture.AssertDiagnostic(t, err, &want)
			}
			if got := draw(); got != first {
				t.Fatal("strict NULL consumed/reseeded", got, first)
			}
			{
				parts := strings.Split(tc.good, ",")
				run(fixture.Select("SELECT id,a="+parts[0]+" AND b="+parts[1]+" AS same FROM "+name, []string{"id", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
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
					{
						want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.errorCode, Message: tc.errorMessage}
						fixture.AssertDiagnostic(t, err, &want)
					}
					if got := draw(); got != first {
						t.Fatal("invalid provider changed stream", got, first)
					}
					{
						parts := strings.Split(tc.good, ",")
						run(fixture.Select("SELECT id,a="+parts[0]+" AND b="+parts[1]+" AS same FROM "+name, []string{"id", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
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
				{
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.errorCode, Message: tc.errorMessage}
					fixture.AssertDiagnostic(t, err, &want)
				}
				if got := draw(); got != want {
					t.Fatal("failed batch provider frontier", got, want)
				}
				{
					parts := strings.Split(tc.good, ",")
					run(fixture.Select("SELECT id,a="+parts[0]+" AND b="+parts[1]+" AS same FROM "+name, []string{"id", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
				}
			}
			run(fixture.Exec("DELETE FROM "+name, 1))
		}
	}
}
