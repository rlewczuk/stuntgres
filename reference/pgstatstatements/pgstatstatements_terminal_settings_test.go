package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsTerminalSettings(t *testing.T) {
	for _, planning := range []string{"off", "on"} {
		for _, route := range []string{"direct", "prepared"} {
			t.Run(planning+"/"+route, func(t *testing.T) {
				pgssCase(t, true, func(s *pgssScenario) {
					base := "pgss_part.pgss_terminal_" + planning + "_" + route
					s.exec("CREATE TABLE " + base + "(k integer)")
					s.run(fixture.Exec("INSERT INTO "+base+" VALUES(7)", 1))
					s.work(fixture.Exec("SET pg_stat_statements.track_planning="+planning, 0))
					q := "SELECT set_config('pg_stat_statements.track','none',false) AS track FROM " + base
					pattern := "SELECT set_config%FROM " + base
					s.planCounts(pattern, 0, 0, 0, 0)
					step := fixture.Select(q, []string{"track"}, []string{"TEXT"}, [][]any{{"none"}})
					step.Prepared = route == "prepared"
					s.work(step)
					plans, groups := int64(0), int64(0)
					if planning == "on" {
						plans, groups = 1, 1
					}
					// Planning already succeeded, independently of the effective
					// track=none observed by ExecutorEnd. SQL itself still succeeds.
					s.planCounts(pattern, groups, plans, 0, 0)
					show := fixture.Select("SHOW pg_stat_statements.track", []string{"pg_stat_statements.track"}, []string{"TEXT"}, [][]any{{"none"}})
					show.Prepared = route == "prepared"
					s.work(show)
					s.work(fixture.Exec("SET pg_stat_statements.track='top'", 0))
					normal := "SELECT k AS pgss_terminal_reenabled FROM " + base
					step = fixture.Select(normal, []string{"pgss_terminal_reenabled"}, []string{"INT4"}, [][]any{{int64(7)}})
					step.Prepared = route == "prepared"
					s.work(step)
					s.planCounts(normal, 1, plans, 1, 1)
					s.planCounts(pattern, groups, plans, 0, 0)
				})
			})
		}
	}
}

func TestReferencePgStatStatementsTerminalInvocation(t *testing.T) {
	for _, restoration := range []string{"session", "invocation", "invocation_session"} {
		for _, planning := range []string{"off", "on"} {
			for _, route := range []string{"direct", "prepared"} {
				t.Run(restoration+"/"+planning+"/"+route, func(t *testing.T) {
					pgssCase(t, true, func(s *pgssScenario) {
						base := "pgss_part.pgss_terminal_" + restoration + "_" + planning + "_" + route
						s.exec("CREATE TABLE " + base + "(k integer)")
						s.run(fixture.Exec("INSERT INTO "+base+" VALUES(7)", 1))
						config, local := "", "false"
						if restoration != "session" {
							config = " SET pg_stat_statements.track='all'"
						}
						if restoration == "invocation" {
							local = "true"
						}
						// A table-dependent SELECT forces a genuine child executor on
						// PostgreSQL as well as native, not a PL simple-expression path.
						s.exec("CREATE FUNCTION " + base + "_fn() RETURNS text LANGUAGE plpgsql" + config + " AS $$DECLARE answer text; BEGIN SELECT /*pgss_terminal_child*/ set_config('pg_stat_statements.track','none'," + local + ") INTO answer FROM " + base + "; RETURN answer; END$$")
						callerTrack := "all"
						if restoration != "session" {
							callerTrack = "top"
						}
						s.work(fixture.Exec("SET pg_stat_statements.track='"+callerTrack+"'", 0))
						s.work(fixture.Exec("SET pg_stat_statements.track_planning="+planning, 0))
						q := "SELECT " + base + "_fn() AS track"
						child := "SELECT /*pgss_terminal_child*/%FROM " + base
						s.planCounts(q, 0, 0, 0, 0)
						s.planCounts(child, 0, 0, 0, 0)
						step := fixture.Select(q, []string{"track"}, []string{"TEXT"}, [][]any{{"none"}})
						step.Prepared = route == "prepared"
						s.work(step)
						plans, childGroups := int64(0), int64(0)
						if planning == "on" {
							plans, childGroups = 1, 1
						}
						// SET LOCAL stays inside the invocation's SAVE environment.
						// A session SET overrides proconfig and escapes that boundary;
						// either child sees none at its own executor completion.
						s.planCounts(child, childGroups, plans, 0, 0)
						s.nestedCounts(child, false, childGroups, 0, 0)
						s.nestedCounts(child, true, 0, 0, 0)
						calls, outerGroups, track := int64(0), childGroups, "none"
						if restoration == "invocation" {
							calls, outerGroups, track = 1, 1, "top"
						}
						s.planCounts(q, outerGroups, plans, calls, calls)
						s.nestedCounts(q, true, outerGroups, calls, calls)
						s.nestedCounts(q, false, 0, 0, 0)
						step = fixture.Select("SHOW pg_stat_statements.track", []string{"pg_stat_statements.track"}, []string{"TEXT"}, [][]any{{track}})
						step.Prepared = route == "prepared"
						s.work(step)
						s.work(fixture.Exec("SET pg_stat_statements.track='all'", 0))
						normal := "SELECT k AS pgss_after_terminal_invocation FROM " + base
						step = fixture.Select(normal, []string{"pgss_after_terminal_invocation"}, []string{"INT4"}, [][]any{{int64(7)}})
						step.Prepared = route == "prepared"
						s.work(step)
						s.planCounts(normal, 1, plans, 1, 1)
						s.planCounts(child, childGroups, plans, 0, 0)
					})
				})
			}
		}
	}
}

func TestReferencePgStatStatementsTerminalUtilityEnable(t *testing.T) {
	for _, route := range []string{"direct", "prepared"} {
		t.Run(route, func(t *testing.T) {
			actor := "pgss_terminal_enable_" + route
			fixture.RoleReferenceCleanup(t, []string{actor})
			pgssCase(t, true, func(s *pgssScenario) {
				s.exec("CREATE ROLE " + actor + " LOGIN SUPERUSER PASSWORD 'role-attributes-fixture'")
				for _, setting := range []string{"track='none'", "track_utility=off", "track_planning=off"} {
					s.exec("ALTER ROLE " + actor + " SET pg_stat_statements." + setting)
				}
				pool := fixture.RoleAttributesPool(t, s.p, s.b, actor)
				defer pool.Close()
				worker, err := pool.Conn(s.p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer worker.Close()
				work := func(step fixture.Step) {
					step.Prepared = route == "prepared"
					fixture.RunStep(s.t, s.p.Ctx, worker, s.b.Recorder, step)
				}
				read := "SELECT COALESCE(sum(s.calls),0)::bigint AS calls,COALESCE(sum(s.rows),0)::bigint AS rows,COALESCE(sum(s.plans),0)::bigint AS plans FROM pgss_part.pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE r.rolname='" + actor + "' AND s.dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
				check := func(calls int64) {
					s.both(fixture.Select(read, []string{"calls", "rows", "plans"}, []string{"INT8", "INT8", "INT8"}, [][]any{{calls, int64(0), int64(0)}}))
				}
				check(0)
				work(fixture.Exec("SET pg_stat_statements.track='top'", 0))
				work(fixture.Exec("SET pg_stat_statements.track_utility=on", 0))
				check(0)
				// Utilities use their separately captured hook-enable decision.
				// Neither disabling switch may erase its own successful event.
				work(fixture.Exec("SET pg_stat_statements.track='none'", 0))
				check(1)
				work(fixture.Exec("SET pg_stat_statements.track='top'", 0))
				check(1)
				work(fixture.Exec("SET pg_stat_statements.track_utility=off", 0))
				check(2)
				work(fixture.Exec("SET pg_stat_statements.track_utility=on", 0))
				check(2)
				work(fixture.Exec("SET pg_stat_statements.track='none'", 0))
				check(3)
				work(fixture.Select("SHOW pg_stat_statements.track", []string{"pg_stat_statements.track"}, []string{"TEXT"}, [][]any{{"none"}}))
				check(3)
			})
		})
	}
}
