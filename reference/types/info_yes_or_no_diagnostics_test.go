package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func infoYesOrNoDiagnostic(input string) fixture.Diagnostic {
	if input == "YESx" {
		return fixture.ControlsReferenceDiagnostic("22001", "value too long for type character varying(3)")
	}
	d := fixture.ControlsReferenceDiagnostic("23514", `value for domain information_schema.yes_or_no violates check constraint "yes_or_no_check"`)
	d.Schema, d.DataType, d.Constraint = "information_schema", "yes_or_no", "yes_or_no_check"
	return d
}

func TestReferenceInfoYesOrNoDiagnostics(t *testing.T) {
	infoYesOrNoScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoYesOrNoSeed(t, p, conn, recorder)
		for _, input := range []string{"abc", "yes", "", "Y", "NO ", "YESx"} {
			for _, route := range []string{"literal", "inferred", "typed-base", "explicit-literal", "explicit-bind"} {
				// Overlength explicit casts succeed and are covered by Values.
				if input == "YESx" && strings.HasPrefix(route, "explicit-") {
					continue
				}
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("input=%q/route=%s/prepared=%v", input, route, prepared), func(t *testing.T) {
						value := "'" + input + "'"
						var args []any
						switch route {
						case "inferred":
							value, args = "$1", []any{input}
						case "typed-base":
							value, args = "$1::varchar", []any{input}
						case "explicit-literal":
							value = "CAST(" + value + " AS information_schema.yes_or_no)"
						case "explicit-bind":
							value, args = "CAST($1::varchar AS information_schema.yes_or_no)", []any{input}
						}
						q := "/*é*/ INSERT INTO public.info_yes_or_no_witness VALUES (90,'YES'),(91," + value + ")"
						s := fixture.ErrorDiagnostic(q, infoYesOrNoDiagnostic(input), args...)
						s.Prepared, s.ErrorStage = prepared, "execute"
						if route == "inferred" {
							s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
							if input == "" {
								s.Diagnostic.Context = "unnamed portal parameter $1 = ''"
							}
						}
						fixture.RunStep(t, p.Ctx, conn, recorder, s)
						infoYesOrNoBoth(t, p, conn, recorder, infoYesOrNoStoredIDs())
					})
				}
			}
		}
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("QueryExplicitCheck/prepared=%v", prepared), func(t *testing.T) {
				s := fixture.ErrorDiagnostic("/*é*/ SELECT CAST('abc' AS information_schema.yes_or_no) AS v", infoYesOrNoDiagnostic("abc"))
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
				fixture.RunStep(t, p.Ctx, conn, recorder, s)
			})
		}
		t.Run("HeldStatementRecovery", func(t *testing.T) {
			q := "INSERT INTO public.info_yes_or_no_witness VALUES ($1,$2)"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			for _, input := range []string{"abc", "YESx"} {
				t.Run(input, func(t *testing.T) {
					s := fixture.ErrorDiagnostic(q, infoYesOrNoDiagnostic(input), 4, input)
					s.ErrorStage = "execute"
					s.Diagnostic.Context = "unnamed portal parameter $2 = '...'"
					fixture.RunStep(t, p.Ctx, held, recorder, s)
				})
				infoYesOrNoBoth(t, p, conn, recorder, infoYesOrNoStoredIDs())
			}
			fixture.RunStep(t, p.Ctx, held, recorder, fixture.Exec(q, 1, 4, "YES"))
			fixture.RunStep(t, p.Ctx, held, recorder, fixture.Exec(q, 1, 5, "NO"))
			infoYesOrNoBoth(t, p, conn, recorder, fixture.Select("SELECT id,v FROM public.info_yes_or_no_witness ORDER BY id", []string{"id", "v"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(1), "YES"}, {int64(2), "NO"}, {int64(3), nil}, {int64(4), "YES"}, {int64(5), "NO"}}))
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DELETE FROM public.info_yes_or_no_witness WHERE id=4 OR id=5", 2))
		})
		t.Run("Precancel", func(t *testing.T) {
			infoYesOrNoPrecancel(t, p, backend)
			infoYesOrNoBoth(t, p, conn, recorder, infoYesOrNoStoredIDs())
		})
	})
}

func infoYesOrNoPrecancel(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, read := range []bool{false, true} {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			accepted := func(err error) bool {
				return errors.Is(err, context.Canceled) || backend.PG && errors.Is(err, driver.ErrBadConn)
			}
			if !read {
				result, err := conn.ExecContext(ctx, "INSERT INTO public.info_yes_or_no_witness VALUES (99,$1)", "YES")
				if result != nil || !accepted(err) {
					t.Fatalf("precanceled INSERT result=%v error=%v", result, err)
				}
				return
			}
			want := infoYesOrNoStored(backend.PG)
			rows, err := conn.QueryContext(ctx, want.Query)
			if rows == nil {
				if !accepted(err) {
					t.Fatalf("precanceled read=%v", err)
				}
				return
			}
			defer rows.Close()
			if !backend.PG || err != nil {
				t.Fatalf("precanceled read exposed cursor: %v", err)
			}
			// pq admission can race database/sql's context watcher; validate any
			// emitted prefix and require the complete seed only on clean EOF.
			columns, err := rows.ColumnTypes()
			if err != nil {
				if accepted(rows.Err()) {
					return
				}
				t.Fatal(err)
			}
			infoYesOrNoColumnTypes(t, columns, true)
			n := 0
			for rows.Next() {
				var value any
				if err := rows.Scan(&value); err != nil {
					if accepted(err) {
						return
					}
					t.Fatal(err)
				}
				if n >= len(want.Rows) {
					t.Fatal("precanceled read returned extra row")
				}
				want.CheckRaw(t, 0, n, value)
				n++
			}
			if err := rows.Err(); err != nil {
				if !accepted(err) {
					t.Fatal(err)
				}
			} else if n != len(want.Rows) {
				t.Fatalf("completed read rows=%d want %d", n, len(want.Rows))
			}
		}()
	}
}
