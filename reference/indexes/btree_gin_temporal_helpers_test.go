package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Independently transcribed from REL_18_6 btree_gin and fixed builtin catalogs.
var temporalCases = []networkCase{
	{"date", "-infinity", "2000-01-01", "infinity", "2000-01-03", "date_cmp", 1082, 1082, 1092, []int64{1095, 1096, 1093, 1098, 1097}},
	{"time", "00:00", "12:00", "24:00", "12:00:00.000002", "time_cmp", 1083, 1083, 1107, []int64{1110, 1111, 1108, 1113, 1112}},
	{"timetz", "00:00+15:59:59", "09:00+01", "24:00-15:59:59", "09:00:00.000002+01", "timetz_cmp", 1266, 1266, 1358, []int64{1552, 1553, 1550, 1555, 1554}},
	{"timestamp", "-infinity", "2000-01-01 00:00", "infinity", "2000-01-01 00:00:00.000002", "timestamp_cmp", 1114, 1114, 2045, []int64{2062, 2063, 2060, 2065, 2064}},
	{"timestamptz", "-infinity", "2000-01-01 00:00+00", "infinity", "2000-01-01 00:00:00.000002+00", "timestamptz_cmp", 1184, 1184, 1314, []int64{1322, 1323, 1320, 1325, 1324}},
	{"interval", "-infinity", "1 mon", "infinity", "31 days", "interval_cmp", 1186, 1186, 1315, []int64{1332, 1333, 1330, 1335, 1334}},
}

const temporalClasses = "('date_ops','time_ops','timetz_ops','timestamp_ops','timestamptz_ops','interval_ops')"
const temporalInputs = "(1082,1083,1266,1114,1184,1186)"

var temporalTruth = [][]int64{{1, 2}, {1, 2, 3, 4}, {3, 4}, {3, 4, 5, 6}, {5, 6}, {1, 2, 5, 6}}

func temporalTexts(tc networkCase) []string {
	switch tc.typ {
	case "date":
		return []string{"-infinity", "1999-12-31", "2000-01-01", "2000-01-01", "2000-01-02", "infinity", ""}
	case "time":
		return []string{"00:00", "11:59:59.999999", "12:00", "12:00", "12:00:00.000001", "24:00", ""}
	case "timetz":
		return []string{"00:00+15:59:59", "08:59:59.999999+01", "09:00+01", "09:00+01", "08:00+00", "24:00-15:59:59", ""}
	case "timestamp":
		return []string{"-infinity", "1999-12-31 23:59:59.999999", "2000-01-01 00:00", "2000-01-01 00:00+05", "2000-01-01 00:00:00.000001", "infinity", ""}
	case "timestamptz":
		return []string{"-infinity", "1999-12-31 23:59:59.999999+00", "2000-01-01 00:00+00", "2000-01-01 01:00+01", "2000-01-01 00:00:00.000001+00", "infinity", ""}
	case "interval":
		return []string{"-infinity", "29 days", "1 mon", "30 days", "30 days 0.000001 seconds", "infinity", ""}
	default:
		panic("unknown temporal case")
	}
}

func temporalSeed(run func(fixture.Step), table string, tc networkCase) {
	var rows []string
	for i, text := range temporalTexts(tc) {
		value := "NULL"
		if text != "" {
			value = networkLiteral(text, tc.typ)
		}
		rows = append(rows, "("+strconv.Itoa(i+1)+","+value+")")
	}
	run(fixture.Exec("INSERT INTO "+table+" VALUES"+strings.Join(rows, ","), 7))
}

// Canonical source representations remain distinct even when comparison agrees.
func temporalCanonical(tc networkCase) []string {
	switch tc.typ {
	case "time":
		return []string{"00:00:00", "11:59:59.999999", "12:00:00", "12:00:00", "12:00:00.000001", "24:00:00", ""}
	case "timetz":
		return []string{"00:00:00+15:59:59", "08:59:59.999999+01", "09:00:00+01", "09:00:00+01", "08:00:00+00", "24:00:00-15:59:59", ""}
	case "interval":
		return []string{"-infinity", "29 days", "1 mon", "30 days", "30 days 00:00:00.000001", "infinity", ""}
	default:
		return temporalTexts(tc)
	}
}

func temporalClock(row int, zoned bool) time.Time {
	if !zoned {
		return []time.Time{
			time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(0, 1, 1, 11, 59, 59, 999999000, time.UTC),
			time.Date(0, 1, 1, 12, 0, 0, 0, time.UTC),
			time.Date(0, 1, 1, 12, 0, 0, 0, time.UTC),
			time.Date(0, 1, 1, 12, 0, 0, 1000, time.UTC),
			time.Date(0, 1, 2, 0, 0, 0, 0, time.UTC),
		}[row]
	}
	return []time.Time{
		time.Date(0, 1, 1, 0, 0, 0, 0, time.FixedZone("", 57599)),
		time.Date(0, 1, 1, 8, 59, 59, 999999000, time.FixedZone("", 3600)),
		time.Date(0, 1, 1, 9, 0, 0, 0, time.FixedZone("", 3600)),
		time.Date(0, 1, 1, 9, 0, 0, 0, time.FixedZone("", 3600)),
		time.Date(0, 1, 1, 8, 0, 0, 0, time.UTC),
		time.Date(0, 1, 2, 0, 0, 0, 0, time.FixedZone("", -57599)),
	}[row]
}

func temporalRows(b fixture.Backend, tc networkCase) [][]any {
	var rows [][]any
	for i, text := range temporalCanonical(tc) {
		var value any = text
		if text == "" {
			value = nil
		} else {
			switch tc.typ {
			case "date":
				value = fixture.DateExpected(b.PG, text)
			case "time":
				if b.PG {
					value = temporalClock(i, false)
				}
			case "timetz":
				if b.PG {
					value = temporalClock(i, true).UTC()
				}
			case "timestamp", "timestamptz":
				if i == 1 {
					value = time.Date(1999, 12, 31, 23, 59, 59, 999999000, time.UTC)
				}
				if i == 2 || i == 3 {
					value = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
				}
				if i == 4 {
					value = time.Date(2000, 1, 1, 0, 0, 0, 1000, time.UTC)
				}
			}
		}
		rows = append(rows, []any{int64(i + 1), value})
	}
	return rows
}

func temporalValueStep(b fixture.Backend, query string, tc networkCase, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, []string{"id", "v"}, []string{"INT4", strings.ToUpper(tc.typ)}, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if column != 1 {
			return false
		}
		fixture.AssertRawValue(t, b.PG, s.Types[1], value)
		want := values[row][1]
		if want == nil {
			if value != nil {
				t.Fatalf("NULL raw=%T %v", value, value)
			}
			return true
		}
		if stamp, ok := value.(time.Time); ok {
			wantStamp, ok := want.(time.Time)
			if !ok || !stamp.Equal(wantStamp) {
				t.Fatalf("%s raw=%v want=%v", tc.typ, stamp, want)
			}
			_, offset := stamp.Zone()
			wantOffset := 0
			if tc.typ == "timetz" {
				_, wantOffset = temporalClock(int(values[row][0].(int64))-1, true).Zone()
			}
			if offset != wantOffset {
				t.Fatalf("%s offset=%d want=%d", tc.typ, offset, wantOffset)
			}
		} else if !reflect.DeepEqual(fixture.Normalize(value), want) {
			t.Fatalf("%s raw=%T %v want=%v", tc.typ, value, value, want)
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		temporalMetadata(t, b, cols, s)
	}
	return s
}

func temporalMetadata(t *testing.T, b fixture.Backend, cols []*sql.ColumnType, s fixture.Step) {
	t.Helper()
	fixture.ScalarAssertDescription(t, cols, s.Columns, s.Types)
	fixture.TemporalMetadata(t, b.PG, cols)
	for i, c := range cols {
		scan := reflect.TypeFor[string]()
		if i == 0 {
			scan = reflect.TypeFor[int64]()
			if b.PG {
				scan = reflect.TypeFor[int32]()
			}
		} else if s.Types[i] == "TIMESTAMP" || s.Types[i] == "TIMESTAMPTZ" || b.PG && s.Types[i] != "INTERVAL" {
			scan = reflect.TypeFor[time.Time]()
		} else if b.PG {
			scan = reflect.TypeFor[any]()
		}
		if c.ScanType() != scan {
			t.Fatalf("%s scan=%v want=%v", c.Name(), c.ScanType(), scan)
		}
		nullable, known := c.Nullable()
		if known != !b.PG || nullable != (!b.PG && i == 1) {
			t.Fatalf("nullable %d=%t/%t", i, nullable, known)
		}
		if n, ok := c.Length(); n != 0 || ok {
			t.Fatalf("length=%d/%t", n, ok)
		}
		if p, scale, ok := c.DecimalSize(); p != 0 || scale != 0 || ok {
			t.Fatalf("decimal=%d,%d/%t", p, scale, ok)
		}
	}
}

func temporalValueRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, s fixture.Step) {
	t.Helper()
	classRoutes(t, p, b, conn, s)
}

func temporalGraph(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, schema string, presentation ...map[string]string) {
	t.Helper()
	ginInstalledGraph(t, p, b, conn, schema, temporalCases, temporalInputs, presentation...)
}

func temporalControlSeed(run func(fixture.Step)) {
	run(fixture.Exec("SET TimeZone='UTC'", 0))
	run(fixture.Exec("SET IntervalStyle='postgres'", 0))
	run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
	var columns, keys []string
	columns = append(columns, "id integer NOT NULL")
	for _, tc := range temporalCases {
		columns = append(columns, tc.typ+" "+tc.typ)
		keys = append(keys, tc.typ+" public."+tc.typ+"_ops")
		run(fixture.Exec("CREATE TABLE temporal_saved_"+tc.typ+"(id integer NOT NULL,v "+tc.typ+")", 0))
		temporalSeed(run, "temporal_saved_"+tc.typ, tc)
		run(fixture.Exec("CREATE INDEX temporal_saved_"+tc.typ+"_i ON temporal_saved_"+tc.typ+" USING gin(v public."+tc.typ+"_ops)", 0))
	}
	run(fixture.Exec("CREATE TABLE temporal_saved("+strings.Join(columns, ",")+")", 0))
	run(fixture.Exec("CREATE INDEX temporal_saved_i ON temporal_saved USING gin("+strings.Join(keys, ",")+")", 0))
	run(fixture.Exec("ALTER INDEX temporal_saved_i SET(fastupdate=off,gin_pending_list_limit=64)", 0))
	for i := range 7 {
		row := []string{strconv.Itoa(i + 1)}
		for _, tc := range temporalCases {
			value := "NULL"
			if text := temporalTexts(tc)[i]; text != "" {
				value = networkLiteral(text, tc.typ)
			}
			row = append(row, value)
		}
		run(fixture.Exec("INSERT INTO temporal_saved VALUES("+strings.Join(row, ",")+")", 1))
	}
}

func temporalControlCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, run func(fixture.Step)) {
	t.Helper()
	for _, tc := range temporalCases {
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, temporalValueStep(b, "SELECT id,v FROM temporal_saved_"+tc.typ+" ORDER BY id", tc, temporalRows(b, tc)))
		for i, op := range []string{"<", "<=", "=", ">=", ">", "<>"} {
			run(ginIDs("SELECT id FROM temporal_saved_"+tc.typ+" WHERE v"+op+"$1::"+tc.typ+" ORDER BY id", temporalTruth[i], tc.boundary))
		}
		run(ginIDs("SELECT id FROM temporal_saved WHERE "+tc.typ+"=$1::"+tc.typ+" ORDER BY id", []int64{3, 4}, tc.boundary))
	}
	run(fixture.Select("SELECT reloptions::text AS options FROM pg_class WHERE oid='temporal_saved_i'::regclass", []string{"options"}, []string{"TEXT"}, [][]any{{"{fastupdate=off,gin_pending_list_limit=64}"}}))
}
