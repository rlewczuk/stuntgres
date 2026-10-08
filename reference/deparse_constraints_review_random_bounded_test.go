//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDeparseConstraintsReviewRandomBoundedConsumers(t *testing.T) {
	for _, tc := range []struct{ name, decl, call, compact, pretty string }{
		{"int", "INT", "random(7,7)", "random(7, 7)", "random(7, 7)"},
		{"bigint", "BIGINT", "random(7::bigint,7::bigint)", "random((7)::bigint, (7)::bigint)", "random(7::bigint, 7::bigint)"},
		{"numeric", "NUMERIC", "random(7::numeric,7::numeric)", "random((7)::numeric, (7)::numeric)", "random(7::numeric, 7::numeric)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, _ bool, run func(referenceStep) referenceObserved) {
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
				run(referenceExec("CREATE TABLE original(id INT,a "+tc.decl+",CONSTRAINT c CHECK("+tc.call+">a))", 0))
				for mode, call := range []string{tc.compact, tc.pretty} {
					expr := "(" + call + " > a)"
					if mode == 1 {
						expr = call + " > a"
					}
					q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
					step := referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
					actual := run(step).rows[0][0].(string)
					run(referencePrepared(step))
					run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(id INT,a %s,CONSTRAINT c%d %s)", mode, tc.decl, mode, actual), 0))
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
								{
									want := referenceCheckViolation(name, constraint, detail)
									want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
									referenceAssertDiagnostic(t, err, &want)
								}
							} else if err != nil {
								t.Fatal(err)
							}
							if got := draw(); got != after {
								t.Fatal("bounded call effect", got, after)
							}
						}
						{
							value := any(int64(6))
							typ := "INT4"
							if tc.decl == "BIGINT" {
								typ = "INT8"
							}
							if tc.decl == "NUMERIC" {
								typ = "NUMERIC"
								value = "6"
							}
							run(referenceSelect("SELECT id,a FROM "+name+" ORDER BY id", []string{"id", "a"}, []string{"INT4", typ}, [][]any{{int64(1), value}, {int64(3), nil}}))
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
						{
							want := referenceCheckViolation(name, constraint, detail)
							want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
							referenceAssertDiagnostic(t, err, &want)
						}
						if got := draw(); got != afterBatch {
							t.Fatal("failed batch calls", got, afterBatch)
						}
						{
							value := any(int64(6))
							typ := "INT4"
							if tc.decl == "BIGINT" {
								typ = "INT8"
							}
							if tc.decl == "NUMERIC" {
								typ = "NUMERIC"
								value = "6"
							}
							run(referenceSelect("SELECT id,a FROM "+name+" ORDER BY id", []string{"id", "a"}, []string{"INT4", typ}, [][]any{{int64(1), value}, {int64(3), nil}}))
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
						{
							want := referenceCheckViolation(name, constraint, detail)
							want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
							referenceAssertDiagnostic(t, err, &want)
						}
						if got := draw(); got != after {
							t.Fatal("failed update call", got, after)
						}
						{
							value := any(int64(6))
							typ := "INT4"
							if tc.decl == "BIGINT" {
								typ = "INT8"
							}
							if tc.decl == "NUMERIC" {
								typ = "NUMERIC"
								value = "6"
							}
							run(referenceSelect("SELECT id,a FROM "+name+" ORDER BY id", []string{"id", "a"}, []string{"INT4", typ}, [][]any{{int64(1), value}, {int64(3), nil}}))
						}
						run(referenceExec("DELETE FROM "+name, 2))
					}
				}
			})
		})
	}
}
