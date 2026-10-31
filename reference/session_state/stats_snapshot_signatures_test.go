package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func statsSnapshotMetadata(t *testing.T, rows *sql.Rows, pg, clear bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 1 {
		t.Fatal(columns, err)
	}
	name, kind, scan := "pg_stat_get_snapshot_timestamp", "TIMESTAMPTZ", reflect.TypeFor[time.Time]()
	if clear {
		name, kind, scan = "pg_stat_clear_snapshot", "VOID", reflect.TypeFor[string]()
	}
	if columns[0].Name() != name || columns[0].DatabaseTypeName() != kind {
		t.Fatal("result metadata", columns[0])
	}
	// pq has no scan-type mapping for VOID, so it reports interface{}.
	if pg && clear {
		scan = reflect.TypeFor[any]()
	}
	if columns[0].ScanType() != scan {
		t.Fatalf("%s ScanType %v want %v", kind, columns[0].ScanType(), scan)
	}
	if _, known := columns[0].Nullable(); known {
		t.Fatal("computed result claimed known nullability")
	}
}
func TestReferenceStatsSnapshotSignatures(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, prepared := range []bool{false, true} {
			for _, clear := range []bool{false, true} {
				query, name, kind, value := statsTimestampSQL, "pg_stat_get_snapshot_timestamp", "TIMESTAMPTZ", any(nil)
				if clear {
					query, name, kind, value = statsClearSQL, "pg_stat_clear_snapshot", "VOID", ""
				}
				step := fixture.Select(query, []string{name}, []string{kind}, [][]any{{value}})
				step.Prepared = prepared
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { statsSnapshotMetadata(t, rows, b.PG, clear) }
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			}
		}
		query := `SELECT oid::bigint AS oid,proname::text AS name,pronargs::bigint AS nargs,prorettype::bigint AS result,proisstrict AS strict,provolatile::text AS volatility,proparallel::text AS parallel,proretset AS is_set,prosrc AS source,prolang::bigint AS language FROM pg_proc WHERE oid IN (2230,3788) ORDER BY oid`
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(query, []string{"oid", "name", "nargs", "result", "strict", "volatility", "parallel", "is_set", "source", "language"}, []string{"INT8", "TEXT", "INT8", "INT8", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "INT8"}, [][]any{{int64(2230), "pg_stat_clear_snapshot", int64(0), int64(2278), false, "v", "r", false, "pg_stat_clear_snapshot", int64(12)}, {int64(3788), "pg_stat_get_snapshot_timestamp", int64(0), int64(1184), true, "s", "r", false, "pg_stat_get_snapshot_timestamp", int64(12)}}))
	})
}
func TestReferenceStatsSnapshotDiagnostics(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, name := range []string{"pg_stat_get_snapshot_timestamp", "pg_stat_clear_snapshot"} {
			for _, tc := range []struct{ suffix, signature string }{{"(1)", "integer"}, {"(true)", "boolean"}, {"('bad')", "unknown"}, {"(NULL::integer)", "integer"}, {"(NULL)", "unknown"}, {"(1,2)", "integer, integer"}, {"(x => 1)", "x => integer"}} {
				want := activityDiagnostic("42883", "function "+name+"("+tc.signature+") does not exist", 8)
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				activityError(t, p, c, "SELECT "+name+tc.suffix, want)
			}
			for _, tc := range []struct{ call, signature string }{{"public." + name + "()", "public." + name + "()"}, {"\"" + strings.ToUpper(name) + "\"()", strings.ToUpper(name) + "()"}, {name + "_missing()", name + "_missing()"}} {
				want := activityDiagnostic("42883", "function "+tc.signature+" does not exist", 8)
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				activityError(t, p, c, "SELECT "+tc.call, want)
			}
			want := activityDiagnostic("42883", "function "+name+"(integer) does not exist", 14)
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			activityError(t, p, c, "/*é*/ SELECT "+name+"(1)", want)
			for _, tc := range []struct{ suffix, message string }{{"(*)", name + "(*) specified, but " + name + " is not an aggregate function"}, {"(DISTINCT 1)", "DISTINCT specified, but " + name + " is not an aggregate function"}, {"(1 ORDER BY 1)", "ORDER BY specified, but " + name + " is not an aggregate function"}, {"() FILTER (WHERE true)", "FILTER specified, but " + name + " is not an aggregate function"}, {"() OVER ()", "OVER specified, but " + name + " is not a window function nor an aggregate function"}} {
				// Wrong signatures take precedence over decorations that carry arguments.
				if tc.suffix == "(DISTINCT 1)" || tc.suffix == "(1 ORDER BY 1)" {
					d := activityDiagnostic("42883", "function "+name+"(integer) does not exist", 8)
					d.Hint = want.Hint
					activityError(t, p, c, "SELECT "+name+tc.suffix, d)
				} else {
					activityError(t, p, c, "SELECT "+name+tc.suffix, activityDiagnostic("42809", tc.message, 8))
				}
			}
			activityError(t, p, c, "SELECT "+name+"(stats_missing_column)", activityDiagnostic("42703", `column "stats_missing_column" does not exist`, 9+len(name)))
			activityError(t, p, c, "SELECT stats_missing_schema."+name+"()", activityDiagnostic("3F000", `schema "stats_missing_schema" does not exist`, 8))
			activityError(t, p, c, "SELECT "+name+"(1::stats_missing_type)", activityDiagnostic("42704", `type "stats_missing_type" does not exist`, 12+len(name)))
		}
		statsNoTimestamp(t, p, c, nil)
	})
}
