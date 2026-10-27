package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsObjectHeldNotices(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT)", 0))
			run(fixture.Exec("CREATE STATISTICS held_s (dependencies) ON b,a FROM stats_t", 0))
			oid := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='held_s'")
			for _, tc := range []struct{ sql, code, message string }{
				{"CREATE STATISTICS IF NOT EXISTS held_s (bogus) ON missing FROM stats_t", "42710", `statistics object "held_s" already exists, skipping`},
				{"DROP STATISTICS IF EXISTS missing_held_s", "00000", `statistics object "missing_held_s" does not exist, skipping`},
			} {
				stmt, err := conn.PrepareContext(p.Ctx, tc.sql)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: tc.sql}
				for _, level := range []string{"notice", "warning", "notice"} {
					run(fixture.Exec("SET client_min_messages="+level, 0))
					for range 2 {
						notices = nil
						fixture.RunStep(t, p.Ctx, held, recorder, fixture.Exec(tc.sql, 0))
						var want []fixture.Diagnostic
						if level == "notice" {
							want = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: tc.code, Message: tc.message}}
						}
						if !reflect.DeepEqual(notices, want) {
							t.Fatalf("held notice at %s: got %#v want %#v", level, notices, want)
						}
						if got := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='held_s'"); got != oid {
							t.Fatal("conditional held execution replaced identity")
						}
						run(statisticsRows([]any{"held_s", "1 2", "{f}", nil, nil}))
					}
				}
			}
		})
	}
}
