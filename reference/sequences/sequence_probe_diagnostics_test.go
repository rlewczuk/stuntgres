package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceProbeDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE s")
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE TABLE t(v INT)")
			for _, tc := range []struct{ q, msg string }{{"SELECT currval('s')", `currval of sequence "s" is not yet defined in this session`}, {"SELECT lastval()", "lastval is not yet defined in this session"}} {
				want := fixture.ControlsReferenceDiagnostic("55000", tc.msg)
				fixture.SequenceProbeError(t, p.Ctx, a, tc.q, &want)
			}
			cases := []struct{ q, code, msg string }{
				{"CREATE SEQUENCE bad INCREMENT 0", "22023", "INCREMENT must not be zero"},
				{"CREATE SEQUENCE bad CACHE 0", "22023", "CACHE (0) must be greater than zero"},
				{"CREATE SEQUENCE bad MINVALUE 5 MAXVALUE 5", "22023", "MINVALUE (5) must be less than MAXVALUE (5)"},
				{"CREATE SEQUENCE bad AS TEXT", "22023", "sequence type must be smallint, integer, or bigint"},
				{"CREATE SEQUENCE bad AS SMALLINT MAXVALUE 32768", "22023", "MAXVALUE (32768) is out of range for sequence data type smallint"},
				{"CREATE SEQUENCE bad START 0", "22023", "START value (0) cannot be less than MINVALUE (1)"},
				{"ALTER SEQUENCE s RESTART 0", "22023", "RESTART value (0) cannot be less than MINVALUE (1)"},
				{"SELECT setval('s',0)", "22003", `setval: value 0 is out of bounds for sequence "s" (1..9223372036854775807)`},
			}
			for _, tc := range cases {
				t.Run(tc.q, func(t *testing.T) {
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
					fixture.SequenceProbeError(t, p.Ctx, a, tc.q, &want)
				})
			}
			for _, q := range []string{
				"SELECT nextval('t')", "SELECT currval('t')", "SELECT setval('t',1)", "SELECT nextval('0')", "SELECT nextval('-')", "SELECT nextval('4294967295')", "SELECT nextval('a.b.c')", "SELECT nextval('bad..name')",
				"SELECT /*é*/ nextval('missing')", "CREATE SEQUENCE s", "CREATE SEQUENCE bad CACHE 1 CACHE 2", "CREATE SEQUENCE bad OWNED BY t", "CREATE SEQUENCE bad OWNED BY t.missing", "ALTER SEQUENCE t RESTART", "DROP SEQUENCE missing", "DROP SEQUENCE t", "ALTER SEQUENCE s AS SMALLINT MINVALUE -32769", "ALTER SEQUENCE s MAXVALUE 1",
			} {
				t.Run(q, func(t *testing.T) { fixture.SequenceProbeError(t, p.Ctx, a, q, nil) })
			}
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 1)
			for _, q := range []string{"ALTER SEQUENCE s INCREMENT 0", "ALTER SEQUENCE s CACHE 0", "ALTER SEQUENCE s AS SMALLINT MINVALUE -32769", "ALTER SEQUENCE s MAXVALUE 1"} {
				if _, err := a.ExecContext(p.Ctx, q); err == nil {
					t.Fatal("invalid ALTER accepted", q)
				}
			}
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 2)
			// Prepared input diagnostics have a portal Context that a literal input lacks.
			stmt, err := a.PrepareContext(p.Ctx, "SELECT nextval($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			var v int64
			err = stmt.QueryRowContext(p.Ctx, "missing").Scan(&v)
			if err == nil {
				t.Fatal("missing parameter accepted")
			}
			parameterWant := fixture.ControlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
			parameterWant.Context = "unnamed portal parameter $1 = '...'"
			fixture.AssertDiagnostic(t, err, &parameterWant)
			for _, q := range []string{"SELECT nextval('missing')", "SELECT /*é*/ nextval('missing')"} {
				stmt, err := a.PrepareContext(p.Ctx, q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed literal Prepare exposed statement")
				}
				if err == nil {
					t.Fatal("missing literal Prepare accepted")
				}
				want := fixture.ControlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
				want.Position = "16"
				if q == "SELECT /*é*/ nextval('missing')" {
					want.Position = "22"
				}
				fixture.AssertDiagnostic(t, err, &want)
			}
		})
	}
}
