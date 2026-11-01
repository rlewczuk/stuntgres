package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceDeparseStatisticsResolutionDiagnostics(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT)", 0))
		const name = "pg_get_statisticsobjdef"
		for _, tc := range []struct{ call, signature string }{
			{name + "()", name + "()"}, {name + "(0,true)", name + "(integer, boolean)"},
			{name + "(NULL,true)", name + "(unknown, boolean)"}, {name + "(true)", name + "(boolean)"},
			{name + "('x'::text)", name + "(text)"}, {name + "(statobj => 0::oid)", name + "(statobj => oid)"},
			{"public." + name + "(0::oid)", "public." + name + "(oid)"},
			{`"PG_GET_STATISTICSOBJDEF"(0::oid)`, "PG_GET_STATISTICSOBJDEF(oid)"},
		} {
			w := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
			w.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			w.Position = "8"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT "+tc.call, w)
				s.SelectRows = true
				if prepared {
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
				} else {
					s.ErrorStage = "execute"
				}
				run(s)
			}
		}
		for _, tc := range []struct{ query, code, message, marker string }{
			{"SELECT " + name + "('bad')", "22P02", `invalid input syntax for type oid: "bad"`, "'bad'"},
			{"SELECT " + name + "('4294967296')", "22003", `value "4294967296" is out of range for type oid`, "'4294967296'"},
			{"SELECT 'été', " + name + "('bad')", "22P02", `invalid input syntax for type oid: "bad"`, "'bad'"},
			{"SELECT absent_schema." + name + "(NULL)", "3F000", `schema "absent_schema" does not exist`, "absent_schema"},
			{"SELECT " + name + "(missing) FROM stats_t", "42703", `column "missing" does not exist`, "missing"},
			{"SELECT " + name + "(NULL::oid,missing) FROM stats_t", "42703", `column "missing" does not exist`, "missing"},
		} {
			w := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			w.Position = fixture.Position(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.marker)]) + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.query, w)
				s.SelectRows = true
				if prepared {
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
				} else {
					s.ErrorStage = "execute"
				}
				run(s)
			}
		}
		held, err := c.PrepareContext(p.Ctx, "SELECT "+name+"($1)")
		if err != nil {
			t.Fatal("parameter must prepare before conversion", err)
		}
		defer held.Close()
		for _, tc := range []struct{ value, code, message string }{
			{"bad", "22P02", `invalid input syntax for type oid: "bad"`},
			{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
		} {
			w := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			w.Context = "unnamed portal parameter $1 = '...'"
			s := fixture.ErrorDiagnostic("SELECT "+name+"($1)", w, tc.value)
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: s.Query}, b.Recorder, s)
		}
	})
}

func TestReferenceDeparseStatisticsMetadata(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT)", 0))
		run(fixture.Exec("CREATE STATISTICS stats_s(mcv,dependencies) ON b,a FROM stats_t", 0))
		id := statisticsDeparseID(t, c, "stats_s")
		for _, empty := range []bool{false, true} {
			q := statisticsDeparseQuery
			if empty {
				q += " WHERE FALSE"
			}
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, arg := range []any{id, nil, int64(0)} {
				var want any
				if arg == id {
					want = statisticsDeparseBase
				}
				s := fixture.DeparseRelationsTextStep(t, b, q, want, arg)
				if empty {
					s.Rows = nil
				}
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
			}
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Select("SELECT pg_get_statisticsobjdef(NULL::oid)", []string{"pg_get_statisticsobjdef"}, []string{"TEXT"}, [][]any{{nil}}))
		for _, operand := range []string{"NULL", "NULL::oid", "0::oid", "'0'", "'stats_t'::regclass::oid", "3415::oid", "4294967295::oid"} {
			// Prove guaranteed-absent IDs within the statistics class, not by
			// guessing the user-object allocator's current next value.
			if operand == "3415::oid" || operand == "4294967295::oid" {
				run(fixture.Select("SELECT oid::bigint FROM pg_statistic_ext WHERE oid="+operand, []string{"oid"}, []string{"INT8"}, nil))
			}
			s := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_statisticsobjdef("+operand+") AS definition", nil)
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("DROP STATISTICS stats_s", 0))
		statisticsDeparseBoth(t, p, b, c, id, nil)
		run(fixture.Select("SELECT p.oid::bigint,p.pronamespace::bigint,p.proargtypes::text,p.prorettype::bigint,p.pronargs,p.prokind,p.proisstrict,p.provolatile,p.proparallel,p.proretset,p.pronargdefaults,p.provariadic::bigint,p.proargnames,p.prosrc FROM pg_proc p WHERE p.pronamespace=11 AND p.proname='pg_get_statisticsobjdef' ORDER BY p.oid", []string{"oid", "pronamespace", "proargtypes", "prorettype", "pronargs", "prokind", "proisstrict", "provolatile", "proparallel", "proretset", "pronargdefaults", "provariadic", "proargnames", "prosrc"}, []string{"INT8", "INT8", "TEXT", "INT8", "INT2", "CHAR", "BOOL", "CHAR", "CHAR", "BOOL", "INT2", "INT8", "_TEXT", "TEXT"}, [][]any{{int64(3415), int64(11), "26", int64(25), int64(1), "f", true, "s", "s", false, int64(0), int64(0), nil, "pg_get_statisticsobjdef"}}))
	})
}
