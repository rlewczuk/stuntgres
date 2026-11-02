package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemRowsDiagnostics(t *testing.T) {
	srCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		prefix := "SELECT * FROM sr TABLESAMPLE system_rows("
		cases := []struct {
			name, suffix, code, message string
			position                    int
			runtime                     bool
		}{
			{"negative", "-1)", "2202H", "sample size must not be negative", 0, true},
			{"null", "NULL)", "2202H", "TABLESAMPLE parameter cannot be null", 0, true},
			{"repeatable", "1) REPEATABLE(0)", "0A000", "tablesample method system_rows does not support REPEATABLE", 30, false},
			{"repeatable_null", "1) REPEATABLE(NULL)", "0A000", "tablesample method system_rows does not support REPEATABLE", 30, false},
			{"arity", "1,2)", "2202H", "tablesample method system_rows requires 1 argument, not 2", 30, false},
			{"empty_arguments", ")", "42601", "syntax error at or near \")\"", 42, false},
			{"typed_text", "'2'::text)", "42804", "argument of TABLESAMPLE must be type bigint, not type text", 42, false},
			{"boolean", "true)", "42804", "argument of TABLESAMPLE must be type bigint, not type boolean", 42, false},
			{"invalid_literal", "'bad')", "22P02", "invalid input syntax for type bigint: \"bad\"", 42, false},
			{"overflow_literal", "'9223372036854775808')", "22003", "value \"9223372036854775808\" is out of range for type bigint", 42, false},
		}
		for _, item := range cases {
			t.Run(item.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
						s := srError(prefix+item.suffix, item.code, item.message, item.position)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared && !item.runtime {
							s.ErrorStage = "prepare"
						}
						srRun(t, p, b, c, s)
					})
				}
			})
		}
		t.Run("utf8_position", func(t *testing.T) {
			srBoth(t, p, b, c, srError("SELECT /*é*/ * FROM sr TABLESAMPLE system_rows(true)", "42804", "argument of TABLESAMPLE must be type bigint, not type boolean", 48))
		})
		for _, cast := range []string{"", "::bigint"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("parameters%s_prepared_%t", cast, prepared), func(t *testing.T) {
					q := prefix + "$1" + cast + ")"
					var executor fixture.Executor = c
					if prepared {
						executor = srHeld(t, p, c, q)
					}
					for _, value := range []struct {
						name          string
						argument      any
						code, message string
					}{
						{"null", nil, "2202H", "TABLESAMPLE parameter cannot be null"},
						{"negative", int64(-1), "2202H", "sample size must not be negative"},
						{"invalid", "bad", "22P02", "invalid input syntax for type bigint: \"bad\""},
						{"overflow", "9223372036854775808", "22003", "value \"9223372036854775808\" is out of range for type bigint"},
					} {
						t.Run(value.name, func(t *testing.T) {
							want := fixture.ControlsReferenceDiagnostic(value.code, value.message)
							if value.name == "invalid" || value.name == "overflow" {
								// PostgreSQL 18.6 redacts Bind values in this Context.
								want.Context = "unnamed portal parameter $1 = '...'"
							}
							srQueryDiagnostic(t, p, executor, q, want, value.argument)
						})
					}
					recovery := fixture.Select(q, srNames, srTypes, srRows(), int64(11))
					recovery.Bag = true
					srRun(t, p, b, executor, recovery)
				})
			}
		}
		t.Run("empty_startup", func(t *testing.T) {
			for _, arg := range []string{"NULL", "-1"} {
				message := "TABLESAMPLE parameter cannot be null"
				if arg == "-1" {
					message = "sample size must not be negative"
				}
				srBoth(t, p, b, c, srError("SELECT * FROM sr_empty TABLESAMPLE system_rows("+arg+")", "2202H", message, 0))
			}
		})
		t.Run("constant_false_frontier", func(t *testing.T) {
			for _, arg := range []string{"NULL", "-1"} {
				srBoth(t, p, b, c, srCount("SELECT count(*) AS n FROM sr TABLESAMPLE system_rows("+arg+") WHERE false", 0))
			}
		})
		t.Run("uninstalled", func(t *testing.T) {
			srRun(t, p, b, c, fixture.Exec("DROP EXTENSION tsm_system_rows", 0))
			s := srError(prefix+"1)", "42704", "tablesample method system_rows does not exist", 30)
			srBoth(t, p, b, c, s)
		})
	})
}

// Unlike fixture.RunStep, this error-only consumer also handles errors surfaced
// by Rows.Err. A mismatch reports the actual transport stage and every exposed
// logical field, rather than treating a late error as successful empty rows.
func srQueryDiagnostic(t *testing.T, p *fixture.Pair, executor fixture.Executor, q string, want fixture.Diagnostic, args ...any) {
	t.Helper()
	rows, err := executor.QueryContext(p.Ctx, q, args...)
	stage := "query"
	if rows != nil {
		defer rows.Close()
	}
	if err == nil && rows != nil {
		stage = "iterate"
		if rows.Next() {
			t.Fatal("failed sample exposed a row")
		}
		err = rows.Err()
	}
	if err == nil {
		t.Fatal("sample argument unexpectedly accepted")
	}
	got := fixture.ExtractDiagnostic(t, err)
	if got != want || stage != "query" {
		t.Fatalf("stage=%s want=query diagnostic=%+v want=%+v", stage, got, want)
	}
}
