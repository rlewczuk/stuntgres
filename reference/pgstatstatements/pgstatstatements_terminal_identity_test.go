package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsTerminalIdentity(t *testing.T) {
	for _, route := range []string{"direct", "prepared"} {
		t.Run(route, func(t *testing.T) {
			actor, target := "pgss_terminal_actor_"+route, "pgss_terminal_target_"+route
			fixture.RoleReferenceCleanup(t, []string{actor, target})
			pgssCase(t, true, func(s *pgssScenario) {
				// Authenticate a fresh actor, rather than SET SESSION AUTHORIZATION
				// from postgres: RESET must return to this independent actor key.
				s.exec("CREATE ROLE " + actor + " LOGIN SUPERUSER PASSWORD 'role-attributes-fixture'")
				s.exec("CREATE ROLE " + target)
				s.exec("GRANT USAGE ON SCHEMA pgss_part TO " + actor + "," + target)
				s.exec("ALTER ROLE " + actor + " SET pg_stat_statements.track='none'")
				s.exec("ALTER ROLE " + actor + " SET pg_stat_statements.track_utility=off")
				s.exec("ALTER ROLE " + actor + " SET pg_stat_statements.track_planning=off")
				pool := fixture.RoleAttributesPool(t, s.p, s.b, actor)
				defer pool.Close()
				worker, err := pool.Conn(s.p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer worker.Close()
				work := func(q string) {
					step := fixture.Exec(q, 0)
					step.Prepared = route == "prepared"
					fixture.RunStep(s.t, s.p.Ctx, worker, s.b.Recorder, step)
				}
				work("SET pg_stat_statements.track='top'")
				work("SET pg_stat_statements.track_utility=on")
				read := "SELECT r.rolname::text AS role,s.queryid IS NOT NULL AS identified,s.query,s.toplevel,s.calls,s.rows FROM pgss_part.pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE r.rolname IN ('" + actor + "','" + target + "') AND s.dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND s.calls>0 ORDER BY r.rolname,s.query"
				names := []string{"role", "identified", "query", "toplevel", "calls", "rows"}
				types := []string{"TEXT", "BOOL", "TEXT", "BOOL", "INT8", "INT8"}
				s.both(fixture.Select(read, names, types, nil))
				setRole := "SET ROLE " + target
				setAuth := "SET SESSION AUTHORIZATION " + target
				// REL_18_6 pgss_store receives no jumble at successful utility
				// completion. An absent terminal-user key gets the original text,
				// not the normalized sticky admission text of the previous user.
				work(setRole)
				s.both(fixture.Select(read, names, types, [][]any{{target, true, setRole, true, int64(1), int64(0)}}))
				work("RESET ROLE")
				s.both(fixture.Select(read, names, types, [][]any{{actor, true, "RESET ROLE", true, int64(1), int64(0)}, {target, true, setRole, true, int64(1), int64(0)}}))
				work(setAuth)
				s.both(fixture.Select(read, names, types, [][]any{{actor, true, "RESET ROLE", true, int64(1), int64(0)}, {target, true, setRole, true, int64(1), int64(0)}, {target, true, setAuth, true, int64(1), int64(0)}}))
				work("RESET SESSION AUTHORIZATION")
				visible := [][]any{{actor, true, "RESET ROLE", true, int64(1), int64(0)}, {actor, true, "RESET SESSION AUTHORIZATION", true, int64(1), int64(0)}, {target, true, setRole, true, int64(1), int64(0)}, {target, true, setAuth, true, int64(1), int64(0)}}
				s.both(fixture.Select(read, names, types, visible))
				// All successful events are under their resulting principal. Early
				// normalization admissions must not become prior-principal calls.
				absence := "SELECT count(*) AS n FROM pgss_part.pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE s.dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND s.calls>0 AND ((r.rolname='" + actor + "' AND s.query LIKE 'SET %') OR (r.rolname='" + target + "' AND s.query LIKE 'RESET %'))"
				s.both(fixture.Select(absence, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				s.exec("SET ROLE " + target)
				defer s.observer.ExecContext(s.p.Ctx, "RESET ROLE")
				// The target sees its own genuine utility text/IDs but cannot see
				// the actor's reset text/IDs. Counters remain exact for both users.
				redacted := [][]any{{actor, false, "<insufficient privilege>", true, int64(1), int64(0)}, {actor, false, "<insufficient privilege>", true, int64(1), int64(0)}, {target, true, setRole, true, int64(1), int64(0)}, {target, true, setAuth, true, int64(1), int64(0)}}
				s.both(fixture.Select(read, names, types, redacted))
				s.exec("RESET ROLE")
				s.both(fixture.Select(read, names, types, visible))
				for _, q := range []string{"SET ROLE pgss_terminal_missing_" + route, "SET SESSION AUTHORIZATION pgss_terminal_missing_" + route} {
					step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: `role "pgss_terminal_missing_` + route + `" does not exist`})
					step.Prepared = route == "prepared"
					fixture.RunStep(s.t, s.p.Ctx, worker, s.b.Recorder, step)
					s.both(fixture.Select(read, names, types, visible))
				}
				step := fixture.Select("SELECT current_user::text,session_user::text", []string{"current_user", "session_user"}, []string{"TEXT", "TEXT"}, [][]any{{actor, actor}})
				step.Prepared = route == "prepared"
				fixture.RunStep(s.t, s.p.Ctx, worker, s.b.Recorder, step)
			})
		})
	}
}

func TestReferencePgStatStatementsTerminalIdentityRepresentative(t *testing.T) {
	for _, route := range []string{"direct", "prepared"} {
		t.Run(route, func(t *testing.T) {
			actor, target := "pgss_terminal_first_actor_"+route, "pgss_terminal_first_target_"+route
			fixture.RoleReferenceCleanup(t, []string{actor, target})
			pgssCase(t, true, func(s *pgssScenario) {
				s.exec("CREATE ROLE " + actor + " LOGIN SUPERUSER PASSWORD 'role-attributes-fixture'")
				s.exec("CREATE ROLE " + target)
				s.exec("GRANT USAGE ON SCHEMA pgss_part TO " + actor + "," + target)
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
				work := func(q string) {
					step := fixture.Exec(q, 0)
					step.Prepared = route == "prepared"
					fixture.RunStep(s.t, s.p.Ctx, worker, s.b.Recorder, step)
				}
				work("SET pg_stat_statements.track='top'")
				work("SET pg_stat_statements.track_utility=on")
				read := "SELECT r.rolname::text AS role,s.queryid<>0 AS identified,s.query,s.toplevel,s.calls,s.rows FROM pgss_part.pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE r.rolname IN ('" + actor + "','" + target + "') AND s.dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND s.calls>0"
				check := func(rows [][]any) {
					step := fixture.Select(read, []string{"role", "identified", "query", "toplevel", "calls", "rows"}, []string{"TEXT", "BOOL", "TEXT", "BOOL", "INT8", "INT8"}, rows)
					step.Bag = true
					s.both(step)
				}
				check(nil)
				// The same-user first event gets its admission-normalized text.
				// A different user's fresh terminal entry instead gets raw text.
				work("SET ROLE " + actor)
				check([][]any{{actor, true, "SET ROLE $1", true, int64(1), int64(0)}})
				work("SET ROLE " + target)
				work("SET ROLE " + actor)
				work("RESET ROLE")
				check([][]any{{actor, true, "SET ROLE $1", true, int64(2), int64(0)}, {actor, true, "RESET ROLE", true, int64(1), int64(0)}, {target, true, "SET ROLE " + target, true, int64(1), int64(0)}})
				work("SET SESSION AUTHORIZATION " + actor)
				work("SET SESSION AUTHORIZATION " + target)
				work("SET SESSION AUTHORIZATION " + actor)
				work("RESET SESSION AUTHORIZATION")
				check([][]any{
					{actor, true, "SET ROLE $1", true, int64(2), int64(0)},
					{actor, true, "RESET ROLE", true, int64(1), int64(0)},
					{actor, true, "SET SESSION AUTHORIZATION $1", true, int64(2), int64(0)},
					{actor, true, "RESET SESSION AUTHORIZATION", true, int64(1), int64(0)},
					{target, true, "SET ROLE " + target, true, int64(1), int64(0)},
					{target, true, "SET SESSION AUTHORIZATION " + target, true, int64(1), int64(0)},
				})
			})
		})
	}
}

func TestReferencePgStatStatementsTerminalIdentityRestoration(t *testing.T) {
	for _, boundary := range []string{"discard", "commit_local", "rollback", "rollback_to"} {
		for _, route := range []string{"direct", "prepared"} {
			t.Run(boundary+"/"+route, func(t *testing.T) {
				actor, target := "pgss_restore_actor_"+boundary+"_"+route, "pgss_restore_target_"+boundary+"_"+route
				fixture.RoleReferenceCleanup(t, []string{actor, target})
				pgssCase(t, true, func(s *pgssScenario) {
					s.exec("CREATE ROLE " + actor + " LOGIN SUPERUSER PASSWORD 'role-attributes-fixture'")
					s.exec("CREATE ROLE " + target)
					s.exec("GRANT USAGE ON SCHEMA pgss_part TO " + actor + "," + target)
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
					defer worker.ExecContext(s.p.Ctx, "ROLLBACK")
					work := func(q string) {
						step := fixture.Exec(q, 0)
						step.Prepared = route == "prepared"
						fixture.RunStep(s.t, s.p.Ctx, worker, s.b.Recorder, step)
					}
					work("SET pg_stat_statements.track='top'")
					work("SET pg_stat_statements.track_utility=on")
					read := "SELECT r.rolname::text AS role,s.queryid IS NOT NULL AS identified,s.query<>'<insufficient privilege>' AS visible,s.toplevel,s.calls,s.rows FROM pgss_part.pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE r.rolname IN ('" + actor + "','" + target + "') AND s.dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND s.calls>0"
					check := func(actorEvents, targetEvents int, redacted bool) {
						var rows [][]any
						for range actorEvents {
							rows = append(rows, []any{actor, !redacted, !redacted, true, int64(1), int64(0)})
						}
						for range targetEvents {
							rows = append(rows, []any{target, true, true, true, int64(1), int64(0)})
						}
						step := fixture.Select(read, []string{"role", "identified", "visible", "toplevel", "calls", "rows"}, []string{"TEXT", "BOOL", "BOOL", "BOOL", "INT8", "INT8"}, rows)
						step.Bag = true
						s.both(step)
					}
					check(0, 0, false)
					actorEvents := 0
					if boundary != "discard" {
						work("BEGIN")
						actorEvents++
					}
					if boundary == "rollback_to" {
						work("SAVEPOINT terminal_saved")
						actorEvents++
					}
					selection := "SET ROLE " + target
					if boundary == "commit_local" {
						selection = "SET LOCAL ROLE " + target
					}
					work(selection)
					check(actorEvents, 1, false)
					terminal := "ROLLBACK"
					switch boundary {
					case "discard":
						terminal = "DISCARD ALL"
					case "commit_local":
						terminal = "COMMIT"
					case "rollback_to":
						terminal = "ROLLBACK TO SAVEPOINT terminal_saved"
					}
					work(terminal)
					targetEvents := 2
					if boundary == "discard" {
						actorEvents++
						targetEvents = 1
					}
					check(actorEvents, targetEvents, false)
					// The utility hook precedes command-end transaction cleanup:
					// COMMIT/ROLLBACK/ROLLBACK TO still belong to target. DISCARD
					// restores identity inside its utility implementation instead.
					s.exec("SET ROLE " + target)
					check(actorEvents, targetEvents, true)
					s.exec("RESET ROLE")
					if boundary == "rollback_to" {
						work("COMMIT")
						actorEvents++
						check(actorEvents, targetEvents, false)
					}
					if boundary != "discard" {
						work("SET pg_stat_statements.track='none'")
					}
					// DISCARD ALL also restores the authenticated actor's disabled
					// defaults; its saved utility enable still charged that event.
					step := fixture.Select("SELECT current_user::text,session_user::text,current_setting('pg_stat_statements.track') AS track", []string{"current_user", "session_user", "track"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{actor, actor, "none"}})
					step.Prepared = route == "prepared"
					fixture.RunStep(s.t, s.p.Ctx, worker, s.b.Recorder, step)
				})
			})
		}
	}
}
