package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsAlterTargets(t *testing.T) {
	withStatisticsAlter(t, func(t *testing.T, s ndistinctScenario) {
		statisticsAlterSetup(s)
		var notices []fixture.Diagnostic
		_, conn, rec := fixture.NoticeReferencePool(t, s.pair, s.pg, &notices)
		run := func(step fixture.Step) { step.Prepared = s.prepared; fixture.RunStep(t, s.pair.Ctx, conn, rec, step) }
		warning := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "lowering statistics target to 10000"}
		for _, tc := range []struct {
			value  string
			target any
			warn   bool
		}{{"0", int64(0), false}, {"10000", int64(10000), false}, {"10001", int64(10000), true}, {"+2147483647", int64(10000), true}, {"-1", nil, false}, {"DEFAULT", nil, false}} {
			notices = nil
			run(fixture.Exec("ALTER STATISTICS public.sa_before SET STATISTICS "+tc.value, 0))
			var want []fixture.Diagnostic
			if tc.warn {
				want = []fixture.Diagnostic{warning}
			}
			if !reflect.DeepEqual(notices, want) {
				t.Fatal("target notice", tc.value, notices, want)
			}
			run(fixture.Select("SELECT stxstattarget FROM pg_statistic_ext WHERE stxname='sa_before'", []string{"stxstattarget"}, []string{"INT2"}, [][]any{{tc.target}}))
			statisticsAlterFacts(t, s, "sa_before")
		}
		for _, tc := range []struct {
			q, code, message string
			want             []fixture.Diagnostic
		}{
			{"ALTER STATISTICS absent_sa SET STATISTICS -2", "22023", "statistics target -2 is too low", nil},
			{"ALTER STATISTICS IF EXISTS absent_sa SET STATISTICS -2", "22023", "statistics target -2 is too low", nil},
			{"ALTER STATISTICS absent_sa SET STATISTICS 10001", "42704", `statistics object "absent_sa" does not exist`, []fixture.Diagnostic{warning}},
		} {
			notices = nil
			run(statisticsError(tc.q, tc.code, tc.message))
			if !reflect.DeepEqual(notices, tc.want) {
				t.Fatal(notices, tc.want)
			}
		}
		notices = nil
		run(fixture.Exec("ALTER STATISTICS IF EXISTS absent_sa SET STATISTICS 10001", 0))
		missing := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `statistics object "absent_sa" does not exist, skipping`}
		if !reflect.DeepEqual(notices, []fixture.Diagnostic{warning, missing}) {
			t.Fatal(notices)
		}
		run(fixture.Exec("SET client_min_messages=error", 0))
		notices = nil
		run(fixture.Exec("ALTER STATISTICS sa_before SET STATISTICS 10001", 0))
		if len(notices) != 0 {
			t.Fatal(notices)
		}
		run(fixture.Exec("RESET client_min_messages", 0))
		statisticsAlterFacts(t, s, "sa_before")
		// An actual later collection, not alteration, consults the reset default.
		mcvTargets(t, s)
	})
}
