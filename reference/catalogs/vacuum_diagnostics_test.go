package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceVacuumDiagnostics(t *testing.T) {
	cases := []struct{ sql, code, message, at, hint string }{
		{"VACUUM (bogus) vacuum_diag", "42601", `unrecognized VACUUM option "bogus"`, "bogus", ""},
		{"/* é */ VACUUM (bogus) vacuum_diag", "42601", `unrecognized VACUUM option "bogus"`, "bogus", ""},
		{"VACUUM (FREEZE 'bad') vacuum_diag", "42601", "freeze requires a Boolean value", "", ""},
		{"VACUUM (FREEZE 'bad',FREEZE false) vacuum_diag", "42601", "freeze requires a Boolean value", "", ""},
		{"VACUUM (VERBOSE 'bad') vacuum_diag", "42601", "verbose requires a Boolean value", "", ""},
		{"VACUUM (ANALYZE 'bad') vacuum_diag", "42601", "analyze requires a Boolean value", "", ""},
		{"VACUUM (FULL 'bad') vacuum_diag", "42601", "full requires a Boolean value", "", ""},
		{"VACUUM (SKIP_LOCKED 'bad') vacuum_diag", "42601", "skip_locked requires a Boolean value", "", ""},
		{"VACUUM (PROCESS_MAIN 'bad') vacuum_diag", "42601", "process_main requires a Boolean value", "", ""},
		{"VACUUM (PROCESS_TOAST 'bad') vacuum_diag", "42601", "process_toast requires a Boolean value", "", ""},
		{"VACUUM (DISABLE_PAGE_SKIPPING 'bad') vacuum_diag", "42601", "disable_page_skipping requires a Boolean value", "", ""},
		{"VACUUM (TRUNCATE 'bad') vacuum_diag", "42601", "truncate requires a Boolean value", "", ""},
		{"VACUUM (SKIP_DATABASE_STATS 'bad') vacuum_diag", "42601", "skip_database_stats requires a Boolean value", "", ""},
		{"VACUUM (ONLY_DATABASE_STATS 'bad')", "42601", "only_database_stats requires a Boolean value", "", ""},
		{"VACUUM (PARALLEL) vacuum_diag", "42601", "parallel option requires a value between 0 and 1024", "PARALLEL", ""},
		{"VACUUM (PARALLEL -1) vacuum_diag", "42601", "parallel workers for vacuum must be between 0 and 1024", "PARALLEL", ""},
		{"VACUUM (PARALLEL 1025) vacuum_diag", "42601", "parallel workers for vacuum must be between 0 and 1024", "PARALLEL", ""},
		{"VACUUM (PARALLEL 'bad') vacuum_diag", "42601", "parallel requires an integer value", "", ""},
		{"VACUUM (BUFFER_USAGE_LIMIT '999999999999999999999kB') vacuum_diag", "22023", "BUFFER_USAGE_LIMIT option must be 0 or between 128 kB and 16777216 kB", "", "Value exceeds integer range."},
		{"VACUUM (BUFFER_USAGE_LIMIT '128 zz') vacuum_diag", "22023", "BUFFER_USAGE_LIMIT option must be 0 or between 128 kB and 16777216 kB", "", `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`},
		{"VACUUM (BUFFER_USAGE_LIMIT 'bogus') vacuum_diag", "22023", "BUFFER_USAGE_LIMIT option must be 0 or between 128 kB and 16777216 kB", "", ""},
		{"VACUUM (INDEX_CLEANUP bogus) vacuum_diag", "42601", "index_cleanup requires a Boolean value", "", ""},
		{"VACUUM (FULL,INDEX_CLEANUP 2) vacuum_diag", "42601", "index_cleanup requires a Boolean value", "", ""},
		{"VACUUM (BUFFER_USAGE_LIMIT 1) vacuum_diag", "22023", "BUFFER_USAGE_LIMIT option must be 0 or between 128 kB and 16777216 kB", "", ""},
		{"VACUUM (BUFFER_USAGE_LIMIT) vacuum_diag", "42601", "buffer_usage_limit requires a parameter", "", ""},
		{"VACUUM (FULL,PARALLEL 1) vacuum_diag", "0A000", "VACUUM FULL cannot be performed in parallel", "", ""},
		{"VACUUM (FULL,BUFFER_USAGE_LIMIT 0) vacuum_diag", "0A000", "BUFFER_USAGE_LIMIT cannot be specified for VACUUM FULL", "", ""},
		{"VACUUM vacuum_diag(id)", "0A000", "ANALYZE option must be specified when a column list is provided", "", ""},
		{"VACUUM (ANALYZE false) vacuum_diag(id)", "0A000", "ANALYZE option must be specified when a column list is provided", "", ""},
		{"VACUUM (FULL,DISABLE_PAGE_SKIPPING) vacuum_diag", "0A000", "VACUUM option DISABLE_PAGE_SKIPPING cannot be used with FULL", "", ""},
		{"VACUUM (FULL,PROCESS_TOAST false) vacuum_diag", "0A000", "PROCESS_TOAST required with VACUUM FULL", "", ""},
		{"VACUUM (ONLY_DATABASE_STATS) vacuum_diag", "0A000", "ONLY_DATABASE_STATS cannot be specified with a list of tables", "", ""},
		{"VACUUM (ONLY_DATABASE_STATS,FREEZE)", "0A000", "ONLY_DATABASE_STATS cannot be specified with other VACUUM options", "", ""},
		{"VACUUM (ONLY_DATABASE_STATS,SKIP_LOCKED)", "0A000", "ONLY_DATABASE_STATS cannot be specified with other VACUUM options", "", ""},
		{"VACUUM vacuum_absent", "42P01", `relation "vacuum_absent" does not exist`, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.sql, func(t *testing.T) {
			vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := vacuumConn(t, p, b)
				vacuumRun(t, p, b, c, fixture.Exec("CREATE TABLE vacuum_diag(id INT)", 0))
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Hint = tc.hint
				if tc.at != "" {
					want.Position = fixture.Position(utf8.RuneCountInString(tc.sql[:strings.Index(tc.sql, tc.at)]) + 1)
				}
				vacuumRun(t, p, b, c, fixture.ErrorDiagnostic(tc.sql, want))
				h := vacuumHeld(t, p, c, tc.sql)
				vacuumRun(t, p, b, h, fixture.ErrorDiagnostic(tc.sql, want))
			})
		})
	}
	for _, begin := range []string{"BEGIN", "BEGIN READ ONLY"} {
		for _, q := range []string{"VACUUM vacuum_absent", "VACUUM (FULL) vacuum_absent", "VACUUM (ANALYZE) vacuum_absent", "VACUUM (PARALLEL 1025) vacuum_absent"} {
			t.Run(begin+"/"+q, func(t *testing.T) {
				vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					c := vacuumConn(t, p, b)
					for _, prepared := range []bool{false, true} {
						vacuumRun(t, p, b, c, fixture.Exec(begin, 0))
						want := fixture.ControlsReferenceDiagnostic("25001", "VACUUM cannot run inside a transaction block")
						if strings.Contains(q, "1025") {
							want = fixture.ControlsReferenceDiagnostic("42601", "parallel workers for vacuum must be between 0 and 1024")
							want.Position = "9"
						}
						s := fixture.ErrorDiagnostic(q, want)
						if prepared {
							vacuumRun(t, p, b, vacuumHeld(t, p, c, q), s)
						} else {
							vacuumRun(t, p, b, c, s)
						}
						vacuumRun(t, p, b, c, fixture.ImplicitError("VACUUM", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
						vacuumRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
					}
				})
			})
		}
	}
	t.Run("native_capabilities_and_syntax", func(t *testing.T) {
		vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := vacuumConn(t, p, b)
			vacuumRun(t, p, b, c, fixture.Exec("CREATE TABLE vacuum_diag(id INT)", 0))
			vacuumRun(t, p, b, c, fixture.Exec("INSERT INTO vacuum_diag VALUES(1)", 1))
			for _, option := range []string{"FULL", "ANALYZE", "FULL,ANALYZE", "ANALYZE,BUFFER_USAGE_LIMIT '128kB'"} {
				q := "VACUUM (" + option + ") vacuum_diag"
				s := fixture.Exec(q, 0)
				if !b.PG {
					message := "VACUUM ANALYZE is not supported"
					if strings.Contains(option, "FULL") {
						message = "VACUUM FULL is not supported"
					}
					s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: message})
				}
				vacuumRun(t, p, b, c, s)
				vacuumRun(t, p, b, vacuumHeld(t, p, c, q), s)
				vacuumRows(t, p, b, c, "vacuum_diag", 1)
			}
			for _, tc := range []struct {
				q, token string
				pos      int
			}{
				{"VACUUM vacuum_diag trailing", "trailing", 20},
				{"PREPARE vacuum_sql AS VACUUM vacuum_diag", "VACUUM", 23},
				{"VACUUM VERBOSE FREEZE vacuum_diag", "FREEZE", 16},
			} {
				want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+tc.token+`"`)
				want.Position = fixture.Position(tc.pos)
				vacuumRun(t, p, b, c, fixture.ErrorDiagnostic(tc.q, want))
			}
		})
	})
}
