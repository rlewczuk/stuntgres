package engine

import (
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestVacuumOptions(t *testing.T) {
	for _, q := range []string{"VACUUM", "VACUUM (FREEZE true,FREEZE false)", "VACUUM (INDEX_CLEANUP AUTO,INDEX_CLEANUP ON,INDEX_CLEANUP OFF)", "VACUUM (PARALLEL 0)", "VACUUM (PARALLEL 1024)", "VACUUM (BUFFER_USAGE_LIMIT '128kB')", "VACUUM (BUFFER_USAGE_LIMIT '16MB')", "VACUUM (BUFFER_USAGE_LIMIT 0)", "VACUUM (FULL false,ANALYZE false)", "VACUUM (ONLY_DATABASE_STATS)"} {
		raw := statement(t, q).(*parser.Vacuum)
		o, err := normalizeVacuum(raw)
		if err != nil {
			t.Fatal(q, err)
		}
		if !o.processMain || !o.processToast || o.freeze || o.full || o.analyze {
			t.Fatalf("defaults/last value %s %#v", q, o)
		}
	}
	for _, q := range []string{
		"VACUUM (ONLY_DATABASE_STATS,PROCESS_MAIN false,PROCESS_TOAST false,VERBOSE)",
		"VACUUM (ONLY_DATABASE_STATS,INDEX_CLEANUP ON,TRUNCATE true,BUFFER_USAGE_LIMIT 0,PARALLEL 2)",
		"VACUUM (ONLY_DATABASE_STATS,SKIP_LOCKED false,FULL false,ANALYZE false,FREEZE false,DISABLE_PAGE_SKIPPING false,SKIP_DATABASE_STATS false)",
	} {
		if _, err := normalizeVacuum(statement(t, q).(*parser.Vacuum)); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, tc := range []struct{ q, code, message, position, hint string }{
		{"VACUUM (FREEZE 'bad',FREEZE true)", "42601", "freeze requires a Boolean value", "", ""},
		{"VACUUM (PARALLEL)", "42601", "parallel option requires a value between 0 and 1024", "9", ""},
		{"VACUUM (PARALLEL 1025)", "42601", "parallel workers for vacuum must be between 0 and 1024", "9", ""},
		{"VACUUM (PARALLEL 'bad')", "42601", "parallel requires an integer value", "", ""},
		{"VACUUM (bogus)", "42601", `unrecognized VACUUM option "bogus"`, "9", ""},
		{"VACUUM (FULL,PARALLEL 1)", "0A000", "VACUUM FULL cannot be performed in parallel", "", ""},
		{"VACUUM (FULL,BUFFER_USAGE_LIMIT 0)", "0A000", "BUFFER_USAGE_LIMIT cannot be specified for VACUUM FULL", "", ""},
		{"VACUUM t(id)", "0A000", "ANALYZE option must be specified when a column list is provided", "", ""},
		{"VACUUM (FULL,DISABLE_PAGE_SKIPPING)", "0A000", "VACUUM option DISABLE_PAGE_SKIPPING cannot be used with FULL", "", ""},
		{"VACUUM (FULL,PROCESS_TOAST false)", "0A000", "PROCESS_TOAST required with VACUUM FULL", "", ""},
		{"VACUUM (ONLY_DATABASE_STATS) t", "0A000", "ONLY_DATABASE_STATS cannot be specified with a list of tables", "", ""},
		{"VACUUM (ONLY_DATABASE_STATS,FREEZE)", "0A000", "ONLY_DATABASE_STATS cannot be specified with other VACUUM options", "", ""},
		{"VACUUM (INDEX_CLEANUP 2)", "42601", "index_cleanup requires a Boolean value", "", ""},
		{"VACUUM (BUFFER_USAGE_LIMIT)", "42601", "buffer_usage_limit requires a parameter", "", ""},
		{"VACUUM (ONLY_DATABASE_STATS,SKIP_LOCKED)", "0A000", "ONLY_DATABASE_STATS cannot be specified with other VACUUM options", "", ""},
		{"VACUUM (BUFFER_USAGE_LIMIT '128 zz')", "22023", "BUFFER_USAGE_LIMIT option must be 0 or between 128 kB and 16777216 kB", "", `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`},
		{"VACUUM (BUFFER_USAGE_LIMIT '999999999999999999999kB')", "22023", "BUFFER_USAGE_LIMIT option must be 0 or between 128 kB and 16777216 kB", "", "Value exceeds integer range."},
	} {
		t.Run(tc.q, func(t *testing.T) {
			_, err := normalizeVacuum(statement(t, tc.q).(*parser.Vacuum))
			var d *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint}
			if !errors.As(err, &d) || *d != *want {
				t.Fatalf("got %#v want %#v", d, want)
			}
		})
	}
}
