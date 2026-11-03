package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func pgTrgmThresholdDiagnostics(t *testing.T) {
	pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := pgTrgmRun(t, p, b, c)
		lookup := func(query, name, types string) {
			s := extensionMissing(query, name, types)
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
		}
		t.Run("BeforeInstall", func(t *testing.T) {
			run = pgTrgmRun(t, p, b, c)
			lookup("SELECT show_limit()", "show_limit", "")
			lookup("SELECT set_limit(0.25::real)", "set_limit", "real")
		})
		run = pgTrgmRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
		run(pgTrgmScalar(b, "SELECT set_limit(0.25::real)", "set_limit", "FLOAT4", float64(0.25)))
		unchanged := func() {
			run(pgTrgmScalar(b, "SELECT show_limit()", "show_limit", "FLOAT4", float64(0.25)))
			pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.25")
		}
		t.Run("Signatures", func(t *testing.T) {
			run = pgTrgmRun(t, p, b, c)
			for _, tc := range []struct{ query, name, types string }{
				{"SELECT show_limit(1)", "show_limit", "integer"},
				{"SELECT set_limit()", "set_limit", ""},
				{"SELECT set_limit(0.25::real,0.5::real)", "set_limit", "real, real"},
				{"SELECT set_limit('0.25'::text)", "set_limit", "text"},
				{"SELECT set_limit(true)", "set_limit", "boolean"},
			} {
				lookup(tc.query, tc.name, tc.types)
				unchanged()
			}
		})
		q := "SELECT set_limit($1::real)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ name, input, message string }{
			{"AboveOne", "2", `2 is outside the valid range for parameter "pg_trgm.similarity_threshold" (0 .. 1)`},
			{"BelowZero", "-0.01", `-0.01 is outside the valid range for parameter "pg_trgm.similarity_threshold" (0 .. 1)`},
			{"PositiveInfinity", "Infinity", `Infinity is outside the valid range for parameter "pg_trgm.similarity_threshold" (0 .. 1)`},
			{"NegativeInfinity", "-Infinity", `-Infinity is outside the valid range for parameter "pg_trgm.similarity_threshold" (0 .. 1)`},
			{"NaN", "NaN", `invalid value for parameter "pg_trgm.similarity_threshold": "NaN"`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				run = pgTrgmRun(t, p, b, c)
				// Empty optional fields are part of the complete expected diagnostic.
				d := fixture.Diagnostic{Code: "22023", Message: tc.message}
				literal := "SELECT set_limit(" + pgTrgmLiteral(tc.input) + "::real)"
				for _, prepared := range []bool{false, true} {
					s := pgTrgmFailure(literal, d)
					s.Prepared = prepared
					run(s)
					unchanged()
					s = pgTrgmFailure(q, d, tc.input)
					s.Prepared = prepared
					run(s)
					unchanged()
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmFailure(q, d, tc.input))
				unchanged()
			})
		}
		t.Run("LiteralInput", func(t *testing.T) {
			run = pgTrgmRun(t, p, b, c)
			d := fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type real: "bad"`, Position: "18"}
			s := pgTrgmFailure("SELECT set_limit('bad')", d)
			run(s)
			unchanged()
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
			unchanged()
		})
		t.Run("PortalInput", func(t *testing.T) {
			run = pgTrgmRun(t, p, b, c)
			d := fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type real: "bad"`, Context: "unnamed portal parameter $1 = '...'"}
			for _, prepared := range []bool{false, true} {
				s := pgTrgmFailure(q, d, "bad")
				s.Prepared = prepared
				run(s)
				unchanged()
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmFailure(q, d, "bad"))
			unchanged()
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmScalar(b, q, "set_limit", "FLOAT4", float64(0.25), "0.25"))
		})
		run = pgTrgmRun(t, p, b, c)
		pgTrgmNotices(t, notices)
	})
}

func TestReferencePgTrgmThresholdAuthority(t *testing.T) {
	pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := pgTrgmRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
		run(pgTrgmScalar(b, "SELECT set_limit(0.25::real)", "set_limit", "FLOAT4", float64(0.25)))
		run(fixture.Exec("CREATE USER trgm_reader NOSUPERUSER", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO trgm_reader", 0))
		run(fixture.Exec("CREATE TABLE trgm_threshold_argument(n int)", 0))
		run(fixture.Exec("INSERT INTO trgm_threshold_argument VALUES(0)", 1))
		run(fixture.Exec("GRANT SELECT ON trgm_threshold_argument TO trgm_reader", 0))
		for _, tc := range []struct{ name, signature, query string }{
			{"set_limit", "real", "SELECT set_limit((1/n)::real) FROM trgm_threshold_argument"},
			{"show_limit", "", "SELECT show_limit()"},
		} {
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.name+"("+tc.signature+") FROM PUBLIC", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION trgm_reader", 0))
			stmt, err := c.PrepareContext(p.Ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
			d := fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.name}
			run(pgTrgmFailure(tc.query, d))
			run(fixture.Prepared(pgTrgmFailure(tc.query, d)))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmFailure(tc.query, d))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.name+"("+tc.signature+") TO trgm_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION trgm_reader", 0))
			if tc.name == "set_limit" {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmFailure(tc.query, fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			} else {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmScalar(b, tc.query, "show_limit", "FLOAT4", float64(0.25)))
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.name+"("+tc.signature+") FROM trgm_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION trgm_reader", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmFailure(tc.query, d))
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.name+"("+tc.signature+") TO PUBLIC", 0))
			run(pgTrgmScalar(b, "SELECT show_limit()", "show_limit", "FLOAT4", float64(0.25)))
		}
		pgTrgmNotices(t, notices)
	})
}
