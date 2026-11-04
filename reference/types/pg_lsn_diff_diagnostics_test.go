package reference_test

import (
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePGLSNDiffDiagnostics(t *testing.T) {
	pgLSNScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE pglsn_diff_empty(a pg_lsn)", 0))
		for _, tc := range []struct{ arguments, signature string }{
			{"1,2", "integer, integer"},
			{"'0/0'::text,'0/0'::pg_lsn", "text, pg_lsn"},
			{"'0/0'::pg_lsn,'0/0'::text", "pg_lsn, text"},
			{"1::numeric,TRUE", "numeric, boolean"},
			{"", ""}, {"'0/0'::pg_lsn", "pg_lsn"},
			{"'0/0'::pg_lsn,'0/0'::pg_lsn,'0/0'::pg_lsn", "pg_lsn, pg_lsn, pg_lsn"},
		} {
			for _, prefix := range []string{"", "/*é*/ "} {
				for _, suffix := range []string{"", " FROM pglsn_diff_empty WHERE FALSE"} {
					q := prefix + "SELECT pg_wal_lsn_diff(" + tc.arguments + ")" + suffix
					d := fixture.ControlsReferenceDiagnostic("42883", "function pg_wal_lsn_diff("+tc.signature+") does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(8 + utf8.RuneCountInString(prefix))
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, d)
						s.SelectRows = true
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
					}
				}
			}
		}
		for _, arguments := range []string{"'bad','0/0'", "'bad',NULL"} {
			for _, prefix := range []string{"", "/*é*/ "} {
				for _, suffix := range []string{"", " FROM pglsn_diff_empty WHERE FALSE"} {
					q := prefix + "SELECT pg_wal_lsn_diff(" + arguments + ")" + suffix
					d := pgLSNDiagnostic("bad")
					d.Position = fixture.Position(24 + utf8.RuneCountInString(prefix))
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, d)
						s.SelectRows = true
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
					}
				}
			}
		}
		q := "SELECT pg_wal_lsn_diff($1,$2)"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("valid inferred signature must prepare", err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			a, b      any
			parameter string
		}{
			{"bad", "0/0", "$1"}, {"0/0", "bad", "$2"},
			{nil, "bad", "$2"}, {"bad", nil, "$1"},
		} {
			d := pgLSNDiagnostic("bad")
			d.Context = "unnamed portal parameter " + tc.parameter + " = '...'"
			// Both extended direct execution and reuse of a successfully prepared
			// statement fail at parameter input, without a statement Position.
			s := fixture.ErrorDiagnostic(q, d, tc.a, tc.b)
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"pg_wal_lsn_diff"}, []string{"NUMERIC"}, [][]any{{"-1"}}, "0/0", "0/1"))
		}
	})
}

func TestReferencePGLSNDiffRoutineMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	// Fixed release identity and all relevant pg_proc defaults are independent
	// of the native executable inventory. Cast textual catalogs explicitly.
	s := fixture.Select("SELECT oid::text,proname::text,pronamespace::text,prokind::text,proargtypes::text,pronargs,prorettype::text,proretset,proisstrict,provolatile::text,proparallel::text,prosecdef,proleakproof,provariadic::text,pronargdefaults,proargnames IS NULL AS unnamed,proallargtypes IS NULL AS no_out,proargmodes IS NULL AS no_modes,proargdefaults IS NULL AS no_defaults,prolang::text,prosrc,probin IS NULL AS no_binary,prosqlbody IS NULL AS no_body,procost,prorows FROM pg_catalog.pg_proc WHERE oid=3165",
		[]string{"oid", "proname", "pronamespace", "prokind", "proargtypes", "pronargs", "prorettype", "proretset", "proisstrict", "provolatile", "proparallel", "prosecdef", "proleakproof", "provariadic", "pronargdefaults", "unnamed", "no_out", "no_modes", "no_defaults", "prolang", "prosrc", "no_binary", "no_body", "procost", "prorows"},
		[]string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT2", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "INT2", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL", "FLOAT4", "FLOAT4"},
		[][]any{{"3165", "pg_wal_lsn_diff", "11", "f", "3220 3220", int64(2), "1700", false, true, "i", "s", false, false, "0", int64(0), true, true, true, true, "12", "pg_wal_lsn_diff", true, true, float64(1), float64(0)}})
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
}
