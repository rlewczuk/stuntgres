package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations come from REL_18_6 transformRangeTableSample, float8 input,
// tablesample_init and system_time_beginsamplescan, not native output.
func TestReferenceSystemTimeDiagnostics(t *testing.T) {
	stCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stDiagnostics(t, p, b, c)
		prefix := "SELECT * FROM sr TABLESAMPLE system_time("
		cases := []struct {
			name, suffix, code, message string
			position int
			runtime bool
		}{
			{"negative", "-1)", "2202H", "sample collection time must not be negative", 0, true},
			{"fraction", "-0.25)", "2202H", "sample collection time must not be negative", 0, true},
			{"negative_infinity", "'-Infinity')", "2202H", "sample collection time must not be negative", 0, true},
			{"nan", "'NaN')", "2202H", "sample collection time must not be negative", 0, true},
			{"null", "NULL)", "2202H", "TABLESAMPLE parameter cannot be null", 0, true},
			{"repeatable", "1) REPEATABLE(0)", "0A000", "tablesample method system_time does not support REPEATABLE", 30, false},
			{"repeatable_null", "1) REPEATABLE(NULL)", "0A000", "tablesample method system_time does not support REPEATABLE", 30, false},
			{"arity", "1,2)", "2202H", "tablesample method system_time requires 1 argument, not 2", 30, false},
			{"empty_arguments", ")", "42601", "syntax error at or near \")\"", 42, false},
			{"typed_text", "'2'::text)", "42804", "argument of TABLESAMPLE must be type double precision, not type text", 42, false},
			{"boolean", "true)", "42804", "argument of TABLESAMPLE must be type double precision, not type boolean", 42, false},
			{"invalid_literal", "'bad')", "22P02", "invalid input syntax for type double precision: \"bad\"", 42, false},
			{"overflow_literal", "'1e999')", "22003", "\"1e999\" is out of range for type double precision", 42, false},
			{"type_before_repeatable", "true) REPEATABLE(0)", "42804", "argument of TABLESAMPLE must be type double precision, not type boolean", 42, false},
			{"negative_before_repeatable", "-1) REPEATABLE(0)", "0A000", "tablesample method system_time does not support REPEATABLE", 30, false},
			{"null_before_repeatable", "NULL) REPEATABLE(0)", "0A000", "tablesample method system_time does not support REPEATABLE", 30, false},
		}
		for _, item := range cases {
			t.Run(item.name, func(t *testing.T) {
				for _, prepared := range []bool{false,true} {
					t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
						s := srError(prefix+item.suffix, item.code, item.message, item.position)
						s.Prepared, s.ErrorStage = prepared, "execute"
						if prepared && !item.runtime { s.ErrorStage = "prepare" }
						srRun(t, p, b, c, s)
					})
				}
			})
		}
		srBoth(t, p, b, c, srError("SELECT /*é*/ * FROM sr TABLESAMPLE system_time(true)", "42804", "argument of TABLESAMPLE must be type double precision, not type boolean", 48))
		for _, cast := range []string{"", "::float8"} {
			for _, prepared := range []bool{false,true} {
				q := prefix+"$1"+cast+")"
				var executor fixture.Executor = c
				if prepared { executor = srHeld(t, p, c, q) }
				for _, item := range []struct { value any; code, message string; bind bool }{
					{nil, "2202H", "TABLESAMPLE parameter cannot be null", false},
					{-1.0, "2202H", "sample collection time must not be negative", false},
					{math.NaN(), "2202H", "sample collection time must not be negative", false},
					{math.Inf(-1), "2202H", "sample collection time must not be negative", false},
					{"bad", "22P02", "invalid input syntax for type double precision: \"bad\"", true},
					{"1e999", "22003", "\"1e999\" is out of range for type double precision", true},
				} {
					want := fixture.ControlsReferenceDiagnostic(item.code, item.message)
					if item.bind { want.Context = "unnamed portal parameter $1 = '...'" }
					srQueryDiagnostic(t, p, executor, q, want, item.value)
					recovery := fixture.Select(q, srNames, srTypes, srRows(), math.Inf(1))
					recovery.Bag = true
					srRun(t, p, b, executor, recovery)
				}
			}
		}
		for _, item := range []struct { argument, message string }{
			{"NULL", "TABLESAMPLE parameter cannot be null"},
			{"-1", "sample collection time must not be negative"},
			{"'NaN'", "sample collection time must not be negative"},
		} {
			srBoth(t, p, b, c, srError("SELECT * FROM sr_empty TABLESAMPLE system_time("+item.argument+")", "2202H", item.message, 0))
		}
		srRun(t, p, b, c, fixture.Exec("DROP EXTENSION tsm_system_time", 0))
		srBoth(t, p, b, c, srError(prefix+"1)", "42704", "tablesample method system_time does not exist", 30))
	})
}

func stDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT * FROM sr TABLESAMPLE system_time($1)"
	h := srHeld(t, p, c, q)
	for _, item := range []struct { value any; message string }{
		{nil, "TABLESAMPLE parameter cannot be null"},
		{-1.0, "sample collection time must not be negative"},
		{math.NaN(), "sample collection time must not be negative"},
	} {
		srQueryDiagnostic(t, p, h, q, fixture.ControlsReferenceDiagnostic("2202H", item.message), item.value)
		s := fixture.Select(q, srNames, srTypes, srRows(), math.Inf(1))
		s.Bag = true
		srRun(t, p, b, h, s)
	}
	srBoth(t, p, b, c, srError("SELECT * FROM sr TABLESAMPLE system_time(1) REPEATABLE(0)", "0A000", "tablesample method system_time does not support REPEATABLE", 30))
}
