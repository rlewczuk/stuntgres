package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgTrgmThresholdAPIs(t *testing.T) {
	t.Run("Calls", pgTrgmThresholdCalls)
	t.Run("Precision", pgTrgmThresholdPrecision)
	t.Run("Diagnostics", pgTrgmThresholdDiagnostics)
}

func pgTrgmThresholdCalls(t *testing.T) {
	pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := pgTrgmRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
		getter := func(value float32) {
			run(pgTrgmScalar(b, "SELECT show_limit()", "show_limit", "FLOAT4", pgTrgmFloat(b.PG, value)))
		}
		getter(0.3)
		q := "SELECT set_limit($1::real) AS value"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		getter(0.3)
		for _, tc := range []struct {
			input string
			value float32
		}{{"0", 0}, {"1", 1}, {"0.25", 0.25}} {
			for _, route := range []string{"direct", "prepared", "held"} {
				// Reset before every route so each execution must actually mutate.
				run(fixture.Exec("SET pg_trgm.similarity_threshold='0.75'", 0))
				s := pgTrgmScalar(b, q, "value", "FLOAT4", pgTrgmFloat(b.PG, tc.value), tc.input)
				switch route {
				case "direct":
					run(s)
				case "prepared":
					run(fixture.Prepared(s))
				case "held":
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				getter(tc.value)
			}
		}
		literalSQL := "SELECT set_limit(0.75::real) AS value"
		literal, err := c.PrepareContext(p.Ctx, literalSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer literal.Close()
		getter(0.25)
		getSQL := "SELECT show_limit() AS value"
		getStmt, err := c.PrepareContext(p.Ctx, getSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer getStmt.Close()
		getHeld := fixture.HeldStatement{Executor: c, Statement: getStmt, SQL: getSQL}
		checkHeld := func(value float32) {
			fixture.RunStep(t, p.Ctx, getHeld, b.Recorder, pgTrgmScalar(b, getSQL, "value", "FLOAT4", pgTrgmFloat(b.PG, value)))
		}
		checkHeld(0.25)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: literal, SQL: literalSQL}, b.Recorder,
			pgTrgmScalar(b, literalSQL, "value", "FLOAT4", float64(0.75)))
		checkHeld(0.75)
		for _, input := range []string{"'0.25'", "0.25::numeric", "0.25::real", "0::integer"} {
			value := float64(0.25)
			if input == "0::integer" {
				value = 0
			}
			run(pgTrgmScalar(b, "SELECT set_limit("+input+")", "set_limit", "FLOAT4", value))
			getter(float32(value))
		}
		run(pgTrgmScalar(b, q, "value", "FLOAT4", float64(0.25), "0.25"))
		for _, prepared := range []bool{false, true} {
			s := pgTrgmScalar(b, q, "value", "FLOAT4", nil, nil)
			s.Prepared = prepared
			run(s)
			getter(0.25)
			for _, query := range []string{"SELECT set_limit(NULL::real)", "SELECT * FROM set_limit(NULL::real)"} {
				s = pgTrgmScalar(b, query, "set_limit", "FLOAT4", nil)
				s.Prepared = prepared
				run(s)
				getter(0.25)
			}
			s = pgTrgmScalar(b, "SELECT * FROM show_limit()", "show_limit", "FLOAT4", float64(0.25))
			s.Prepared = prepared
			run(s)
			s = pgTrgmSelect(b, "SELECT set_limit(0.75::real) AS value WHERE false", []string{"value"}, []string{"FLOAT4"}, nil)
			s.Prepared = prepared
			run(s)
			getter(0.25)
			s = pgTrgmSelect(b, "SELECT show_limit() AS value WHERE false", []string{"value"}, []string{"FLOAT4"}, nil)
			s.Prepared = prepared
			run(s)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmScalar(b, q, "value", "FLOAT4", nil, nil))
		checkHeld(0.25)
		run(pgTrgmScalar(b, "SELECT * FROM set_limit(0.5::real)", "set_limit", "FLOAT4", float64(0.5)))
		checkHeld(0.5)
		pgTrgmNotices(t, notices)
	})
}

func pgTrgmThresholdPrecision(t *testing.T) {
	pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := pgTrgmRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
		check := func(value float32, match bool) {
			run(pgTrgmScalar(b, "SELECT show_limit()", "show_limit", "FLOAT4", pgTrgmFloat(b.PG, value)))
			run(pgTrgmScalar(b, "SELECT 'a' % 'aa' AS match", "match", "BOOL", match))
		}
		for _, prepared := range []bool{false, true} {
			s := pgTrgmScalar(b, "SELECT set_limit(0.250000001::real)", "set_limit", "FLOAT4", float64(0.25))
			s.Prepared = prepared
			run(s)
			check(0.25, true)
			run(fixture.Exec("SET pg_trgm.similarity_threshold='0.250000001'", 0))
			check(0.25, false)
			s = pgTrgmScalar(b, "SELECT set_limit(1.000000001::real)", "set_limit", "FLOAT4", float64(1))
			s.Prepared = prepared
			run(s)
			check(1, false)
			run(fixture.ImplicitError("SET pg_trgm.similarity_threshold='1.000000001'", fixture.Diagnostic{
				Code: "22023", Message: `1 is outside the valid range for parameter "pg_trgm.similarity_threshold" (0 .. 1)`,
			}))
			check(1, false)
		}
		q := "SELECT set_limit($1::real) AS value"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			digits, input, installed string
			value                    float32
		}{
			{"-5", "0.26", "0.3", 0.3},
			{"-4", "0.26", "0.26", 0.26},
			{"0", "0.12345678", "0.123457", 0.123457},
			{"1", "0.26", "0.26", 0.26},
		} {
			for _, route := range []string{"direct", "prepared", "held"} {
				run(fixture.Exec("SET extra_float_digits="+pgTrgmLiteral(tc.digits), 0))
				s := pgTrgmScalar(b, q, "value", "FLOAT4", pgTrgmFloat(b.PG, tc.value), tc.input)
				switch route {
				case "direct":
					run(s)
				case "prepared":
					run(fixture.Prepared(s))
				case "held":
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				pgTrgmSettingValue(run, pgTrgmSettings[0].name, tc.installed)
				// Observe the stored value with normal output, not rounded transport.
				run(fixture.Exec("RESET extra_float_digits", 0))
				check(tc.value, tc.value <= 0.25)
			}
		}
		run(fixture.Exec("CREATE FUNCTION trgm_precision_child(text) RETURNS real LANGUAGE SQL VOLATILE AS $$SELECT set_config('extra_float_digits',$1,false); SELECT 0.26::real$$", 0))
		nestedSQL := "SELECT set_limit(trgm_precision_child($1::text)) AS value"
		nestedStmt, err := c.PrepareContext(p.Ctx, nestedSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer nestedStmt.Close()
		nested := fixture.HeldStatement{Executor: c, Statement: nestedStmt, SQL: nestedSQL}
		for _, tc := range []struct {
			before, child, installed string
			value                    float32
		}{{"1", "-5", "0.3", 0.3}, {"-5", "1", "0.26", 0.26}} {
			for _, route := range []string{"direct", "prepared", "held"} {
				run(fixture.Exec("SET extra_float_digits="+pgTrgmLiteral(tc.before), 0))
				s := pgTrgmScalar(b, nestedSQL, "value", "FLOAT4", pgTrgmFloat(b.PG, tc.value), tc.child)
				switch route {
				case "direct":
					run(s)
				case "prepared":
					run(fixture.Prepared(s))
				case "held":
					fixture.RunStep(t, p.Ctx, nested, b.Recorder, s)
				}
				pgTrgmSettingValue(run, "extra_float_digits", tc.child)
				pgTrgmSettingValue(run, pgTrgmSettings[0].name, tc.installed)
				run(fixture.Exec("RESET extra_float_digits", 0))
				check(tc.value, false)
			}
		}
		pgTrgmNotices(t, notices)
	})
}
