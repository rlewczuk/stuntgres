package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatsSnapshotSettings(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"stats_snapshot_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct {
			name, boot, kind, access, description string
			enums                                 any
		}{
			{"stats_fetch_consistency", "cache", "enum", "user", "Sets the consistency of accesses to statistics data.", "{none,cache,snapshot}"},
			{"track_activities", "on", "bool", "superuser", "Collects information about executing commands.", nil},
			{"track_counts", "on", "bool", "superuser", "Collects statistics on database activity.", nil},
			{"track_functions", "none", "enum", "superuser", "Collects function-level statistics on database activity.", "{none,pl,all}"},
			{"track_io_timing", "off", "bool", "superuser", "Collects timing statistics for database I/O activity.", nil},
			{"track_wal_io_timing", "off", "bool", "superuser", "Collects timing statistics for WAL I/O activity.", nil},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT name,setting,boot_val,reset_val,vartype,context,category,short_desc,enumvals,source FROM pg_settings WHERE name=$1", []string{"name", "setting", "boot_val", "reset_val", "vartype", "context", "category", "short_desc", "enumvals", "source"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "TEXT"}, [][]any{{tc.name, tc.boot, tc.boot, tc.boot, tc.kind, tc.access, "Statistics / Cumulative Query and Index Statistics", tc.description, tc.enums, "default"}}, tc.name)
				s.Prepared = prepared
				run(s)
				s = fixture.Select("SHOW "+tc.name, []string{tc.name}, []string{"TEXT"}, [][]any{{tc.boot}})
				s.Prepared = prepared
				run(s)
				s = settingsOptionsFlags(b.PG, "SELECT pg_catalog.pg_settings_get_flags($1)", "{}", tc.name)
				s.Prepared = prepared
				run(s)
			}
		}
		for _, tc := range []struct{ name, values string }{{"stats_fetch_consistency", "none, cache, snapshot"}, {"track_functions", "none, pl, all"}} {
			d := fixture.ControlsReferenceDiagnostic("22023", `invalid value for parameter "`+tc.name+`": "bad"`)
			d.Hint = "Available values: " + tc.values + "."
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SET "+tc.name+"='bad'", d)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
			}
		}
		statsExec(t, p, b, c, "CREATE ROLE stats_snapshot_reader", 0)
		statsExec(t, p, b, c, "SET ROLE stats_snapshot_reader", 0)
		defer c.ExecContext(context.Background(), "RESET ROLE")
		for _, name := range []string{"track_activities", "track_counts", "track_functions", "track_io_timing", "track_wal_io_timing"} {
			// Authority precedes even an invalid value's parser.
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SET "+name+"='bad'", fixture.ControlsReferenceDiagnostic("42501", `permission denied to set parameter "`+name+`"`))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
			}
		}
		statsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		statsExec(t, p, b, c, "RESET ROLE", 0)
		statsExec(t, p, b, c, "GRANT SET ON PARAMETER track_counts TO stats_snapshot_reader", 0)
		statsExec(t, p, b, c, "SET ROLE stats_snapshot_reader", 0)
		statsExec(t, p, b, c, "SET track_counts=off", 0)
		run(fixture.Select("SHOW track_counts", []string{"track_counts"}, []string{"TEXT"}, [][]any{{"off"}}))
		statsExec(t, p, b, c, "RESET track_counts", 0)
		statsExec(t, p, b, c, "RESET ROLE", 0)
		statsExec(t, p, b, c, "REVOKE SET ON PARAMETER track_counts FROM stats_snapshot_reader", 0)
	})
}
