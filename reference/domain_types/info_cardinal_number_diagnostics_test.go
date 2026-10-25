package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func infoCardinalNumberDiagnostic(input string) fixture.Diagnostic {
	switch input {
	case "bad":
		return fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
	case "2147483648":
		return fixture.ControlsReferenceDiagnostic("22003", `value "2147483648" is out of range for type integer`)
	case "numeric-overflow":
		return fixture.ControlsReferenceDiagnostic("22003", "integer out of range")
	}
	d := fixture.ControlsReferenceDiagnostic("23514", `value for domain information_schema.cardinal_number violates check constraint "cardinal_number_domain_check"`)
	d.Schema, d.DataType, d.Constraint = "information_schema", "cardinal_number", "cardinal_number_domain_check"
	return d
}

func TestReferenceInfoCardinalNumberDiagnostics(t *testing.T) {
	infoCardinalNumberScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoCardinalNumberSeed(t, p, conn, recorder)
		for _, input := range []string{"-1", "bad", "2147483648", "numeric-overflow"} {
			for _, route := range []string{"literal", "inferred", "typed-base", "explicit-cast", "explicit-colon", "explicit-bind"} {
				if input == "numeric-overflow" && route != "literal" && route != "explicit-cast" && route != "explicit-colon" {
					continue
				}
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("input=%s/route=%s/prepared=%v", input, route, prepared), func(t *testing.T) {
						value := "'" + input + "'"
						if input == "-1" {
							value = "-1"
						}
						if input == "numeric-overflow" {
							value = "2147483648"
						}
						var args []any
						switch route {
						case "inferred":
							value, args = "$1", []any{input}
						case "typed-base":
							value, args = "$1::integer", []any{input}
						case "explicit-cast":
							value = "CAST(" + value + " AS information_schema.cardinal_number)"
						case "explicit-colon":
							value = "(" + value + ")::information_schema.cardinal_number"
						case "explicit-bind":
							value, args = "CAST($1::integer AS information_schema.cardinal_number)", []any{input}
						}
						s := fixture.ErrorDiagnostic("/*é*/ INSERT INTO public.info_cardinal_number_witness VALUES (90,7),(91,"+value+")", infoCardinalNumberDiagnostic(input), args...)
						s.Prepared, s.ErrorStage = prepared, "execute"
						if input == "bad" || input == "2147483648" {
							switch route {
							case "literal":
								s.Diagnostic.Position = "73"
							case "explicit-cast":
								s.Diagnostic.Position = "78"
							case "explicit-colon":
								s.Diagnostic.Position = "74"
							}
							if prepared && s.Diagnostic.Position != "" {
								s.ErrorStage = "prepare"
							}
						}
						if route == "inferred" || ((route == "typed-base" || route == "explicit-bind") && input != "-1") {
							s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
						}
						fixture.RunStep(t, p.Ctx, conn, recorder, s)
						infoCardinalNumberBoth(t, p, conn, recorder, infoCardinalNumberStoredIDs())
					})
				}
			}
		}
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("QueryExplicitCheck/prepared=%v", prepared), func(t *testing.T) {
				s := fixture.ErrorDiagnostic("/*é*/ SELECT CAST(-1 AS information_schema.cardinal_number) AS v", infoCardinalNumberDiagnostic("-1"))
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
				fixture.RunStep(t, p.Ctx, conn, recorder, s)
			})
		}
		t.Run("HeldStatementRecovery", func(t *testing.T) {
			q := "INSERT INTO public.info_cardinal_number_witness VALUES ($1,$2)"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			for _, input := range []string{"-1", "bad", "2147483648"} {
				t.Run(input, func(t *testing.T) {
					s := fixture.ErrorDiagnostic(q, infoCardinalNumberDiagnostic(input), 4, input)
					s.ErrorStage = "execute"
					s.Diagnostic.Context = "unnamed portal parameter $2 = '...'"
					fixture.RunStep(t, p.Ctx, held, recorder, s)
					infoCardinalNumberBoth(t, p, conn, recorder, infoCardinalNumberStoredIDs())
				})
			}
			fixture.RunStep(t, p.Ctx, held, recorder, fixture.Exec(q, 1, 4, int64(7)))
			fixture.RunStep(t, p.Ctx, held, recorder, fixture.Exec(q, 1, 5, nil))
			infoCardinalNumberBoth(t, p, conn, recorder, fixture.Select("SELECT id,v FROM public.info_cardinal_number_witness ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}, {int64(2), int64(7)}, {int64(3), nil}, {int64(4), int64(7)}, {int64(5), nil}}))
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DELETE FROM public.info_cardinal_number_witness WHERE id=4 OR id=5", 2))
		})
		t.Run("Precancel", func(t *testing.T) {
			infoCardinalNumberPrecancel(t, p, backend)
			infoCardinalNumberBoth(t, p, conn, recorder, infoCardinalNumberStoredIDs())
		})
	})
}
