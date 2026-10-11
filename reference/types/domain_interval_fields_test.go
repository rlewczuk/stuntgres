package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDomainIntervalFieldInput(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, conn, recorder, s)
		}
		run(fixture.Exec("SET IntervalStyle TO 'postgres'", 0))
		// Independently specified displays also match pinned PostgreSQL 18.0.
		// Bare units and two-field clocks expose input interpretation; the last
		// clock also checks field interpretation followed by precision adjustment.
		cases := []struct {
			name, base, input, output string
		}{
			{"days", "interval day", "1", "1 day"},
			{"hours", "interval hour", "1", "01:00:00"},
			{"months", "interval month", "1", "1 mon"},
			{"day_hour_bare", "interval day to hour", "1", "01:00:00"},
			{"day_hour_clock", "interval day to hour", "1:30", "01:00:00"},
			{"minute_second_bare", "interval minute to second(2)", "1", "00:00:01"},
			{"minute_second_clock", "interval minute to second(2)", "1:30.126", "00:01:30.13"},
			{"days_chain", "domain_s.field_days", "1", "1 day"},
			{"hours_chain", "domain_s.field_hours", "1", "01:00:00"},
			{"months_chain", "domain_s.field_months", "1", "1 mon"},
			{"range_chain", "domain_s.field_minute_second_clock", "1:30.126", "00:01:30.13"},
		}
		checks := make([]fixture.Step, 0, len(cases))
		for _, tc := range cases {
			domain := "domain_s.field_" + tc.name
			table := "domain_s.field_values_" + tc.name
			run(fixture.Exec("CREATE DOMAIN "+domain+" AS "+tc.base, 0))
			run(fixture.Exec("CREATE TABLE "+table+"(id integer,v "+domain+")", 0))
			t.Run(tc.name, func(t *testing.T) {
				want := make([][]any, 0, 10)
				id := int64(0)
				for _, prepared := range []bool{false, true} {
					for _, expression := range []string{"'" + tc.input + "'::" + domain, "CAST('" + tc.input + "' AS " + domain + ")"} {
						s := fixture.Select("SELECT "+expression+" AS v FROM public.one", []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.output}})
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, conn, recorder, s)
					}
					for _, expression := range []string{"$1::" + domain, "CAST($1 AS " + domain + ")"} {
						s := fixture.Select("SELECT "+expression+" AS v FROM public.one", []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.output}}, tc.input)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, conn, recorder, s)
						null := fixture.Select("SELECT "+expression+" AS v FROM public.one", []string{"v"}, []string{"INTERVAL"}, [][]any{{nil}}, nil)
						null.Prepared = prepared
						fixture.RunStep(t, p.Ctx, conn, recorder, null)
					}
					for _, value := range []string{"'" + tc.input + "'", "'" + tc.input + "'::" + domain, "CAST('" + tc.input + "' AS " + domain + ")"} {
						id++
						s := fixture.Exec("INSERT INTO "+table+" VALUES($1,"+value+")", 1, id)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, conn, recorder, s)
						want = append(want, []any{id, tc.output})
					}
					id++
					s := fixture.Exec("INSERT INTO "+table+" VALUES($1,$2)", 1, id, tc.input)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, recorder, s)
					want = append(want, []any{id, tc.output})
					id++
					null := fixture.Exec("INSERT INTO "+table+" VALUES($1,$2)", 1, id, nil)
					null.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, recorder, null)
					want = append(want, []any{id, nil})
				}
				s := fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INTERVAL"}, want)
				fixture.RunStep(t, p.Ctx, conn, recorder, s)
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(s))
				checks = append(checks, s)
			})
		}
		if backend.PG {
			return // PostgreSQL has no native logical Dump/Reset/Restore API.
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET IntervalStyle TO 'postgres'", 0))
		for _, s := range checks {
			run(s)
			run(fixture.Prepared(s))
		}
	})
}
