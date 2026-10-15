package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

const extractA = "c232ab00-9414-11ec-b3c8-9f6bdeced846"
const extractB = "017f22e2-79b0-7cc3-98c4-dc0c0c07398f"
const extractC = "00000000-0000-4000-8000-000000000000"
const extractQuery = "SELECT uuid_extract_timestamp($1) AS ts,uuid_extract_version($1) AS ver"

var extractTime = time.Date(2022, 2, 22, 19, 22, 22, 0, time.UTC)

func extractScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		body(p, b)
	}
}

func extractStep(q string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, []string{"ts", "ver"}, []string{"TIMESTAMPTZ", "INT2"}, rows, args...)
	s.CheckRaw = func(*testing.T, int, int, any) bool { return false }
	return s
}

func extractRunBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
}

func extractBase(t *testing.T, p *fixture.Pair, b fixture.Backend) fixture.Step {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE uuid_extract_values(id integer,v uuid)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO uuid_extract_values VALUES (1,'"+extractA+"'),(2,'"+extractB+"'),(3,'"+extractC+"'),(4,NULL)", 4))
	s := extractStep("SELECT uuid_extract_timestamp(v) AS ts,uuid_extract_version(v) AS ver FROM uuid_extract_values ORDER BY id", [][]any{{extractTime, int64(1)}, {extractTime, int64(7)}, {nil, int64(4)}, {nil, nil}})
	extractRunBoth(t, p, b, s)
	return s
}

func TestReferenceUUIDExtractValues(t *testing.T) {
	extractScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		extractBase(t, p, b)
		for _, name := range []string{"uuid_extract_", "pg_catalog.uuid_extract_"} {
			q := "SELECT " + name + "timestamp('" + extractA + "') AS ts," + name + "version('" + extractA + "') AS ver"
			extractRunBoth(t, p, b, extractStep(q, [][]any{{extractTime, int64(1)}}))
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE extract_identifiers(uuid_extract_version integer,uuid_extract_timestamp integer)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO extract_identifiers VALUES (1,7)", 1))
		extractRunBoth(t, p, b, fixture.Select("SELECT uuid_extract_version,uuid_extract_timestamp FROM extract_identifiers", []string{"uuid_extract_version", "uuid_extract_timestamp"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}}))
		extractRunBoth(t, p, b, extractStep(`SELECT "uuid_extract_timestamp"(NULL) AS ts,"uuid_extract_version"(NULL) AS ver`, [][]any{{nil, nil}}))
	})
}

func extractVersions(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for version := range 16 {
		for _, variant := range []string{"8", "9", "a", "b", "0", "7", "c", "e", "f"} {
			u := fmt.Sprintf("00000000-0000-%x000-%s000-000000000000", version, variant)
			var ts, ver any
			if variant == "8" || variant == "9" || variant == "a" || variant == "b" {
				ver = int64(version)
				if version == 1 {
					ts = time.Date(1582, 10, 15, 0, 0, 0, 0, time.UTC)
				}
				if version == 7 {
					ts = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
				}
			}
			extractRunBoth(t, p, b, extractStep(extractQuery, [][]any{{ts, ver}}, u))
		}
	}
	for _, u := range []any{uuidZ, uuidF, nil} {
		extractRunBoth(t, p, b, extractStep(extractQuery, [][]any{{nil, nil}}, u))
	}
}

func TestReferenceUUIDExtractVersions(t *testing.T) {
	extractScenario(t, func(p *fixture.Pair, b fixture.Backend) { extractVersions(t, p, b) })
}

func extractTimestamps(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	gregorian := time.Date(1582, 10, 15, 0, 0, 0, 0, time.UTC)
	unix := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		uuid    string
		instant time.Time
		version int64
	}{
		{extractA, extractTime, 1}, {extractB, extractTime, 7},
		{"00000000-0000-1000-8000-000000000000", gregorian, 1},
		{"00000009-0000-1000-8000-000000000000", gregorian, 1},
		{"0000000a-0000-1000-8000-000000000000", gregorian.Add(time.Microsecond), 1},
		{"13814000-1dd2-11b2-8000-000000000000", unix, 1},
		{"13813fff-1dd2-11b2-8000-000000000000", unix.Add(-time.Microsecond), 1},
		{"13814009-1dd2-11b2-8000-000000000000", unix, 1},
		{"1381400a-1dd2-11b2-8000-000000000000", unix.Add(time.Microsecond), 1},
		{"00000000-0000-7000-8000-000000000000", unix, 7},
		{"00000000-0000-7fff-bfff-ffffffffffff", unix, 7},
		{"00000000-0001-7fff-8000-000000000000", unix.Add(time.Millisecond), 7},
		{"c232ab00-9414-11ec-8000-000000000000", extractTime, 1},
		{"c232ab00-9414-11ec-bfff-ffffffffffff", extractTime, 1},
	} {
		extractRunBoth(t, p, b, extractStep(extractQuery, [][]any{{tc.instant, tc.version}}, tc.uuid))
	}
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer c.ExecContext(p.Ctx, "SET TIME ZONE 'UTC'")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, extractStep(extractQuery, [][]any{{extractTime, int64(7)}}, extractB))
}

func TestReferenceUUIDExtractTimestamps(t *testing.T) {
	extractScenario(t, func(p *fixture.Pair, b fixture.Backend) { extractTimestamps(t, p, b) })
}

func extractMetadata(t *testing.T, b fixture.Backend, columns []*sql.ColumnType) {
	t.Helper()
	fixture.ScalarAssertDescription(t, columns, []string{"ts", "ver"}, []string{"TIMESTAMPTZ", "INT2"})
	fixture.ScalarAssertMetadata(t, b.PG, columns, 0, 1)
	fixture.ScalarAssertUnknownLength(t, columns, 0, 1)
	if columns[0].ScanType() != reflect.TypeFor[time.Time]() {
		t.Fatal("timestamp ScanType", columns[0].ScanType())
	}
	for _, c := range columns {
		if precision, scale, known := c.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("unexpected precision", precision, scale, known)
		}
	}
}

func TestReferenceUUIDExtractMetadata(t *testing.T) {
	extractScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		extractBase(t, p, b)
		for _, q := range []string{
			"SELECT uuid_extract_timestamp(v) AS ts,uuid_extract_version(v) AS ver FROM uuid_extract_values ORDER BY id",
			"SELECT uuid_extract_timestamp(v) AS ts,uuid_extract_version(v) AS ver FROM uuid_extract_values WHERE id=4",
			"SELECT uuid_extract_timestamp(v) AS ts,uuid_extract_version(v) AS ver FROM uuid_extract_values WHERE id=0",
		} {
			for _, prepared := range []bool{false, true} {
				func() {
					var e fixture.Executor = b.DB
					if prepared {
						s, err := b.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer s.Close()
						e = fixture.HeldStatement{Executor: b.DB, Statement: s, SQL: q}
					}
					rows, err := e.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), q)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					extractMetadata(t, b, columns)
					got := [][]any{}
					for rows.Next() {
						var ts, ver any
						if err := rows.Scan(&ts, &ver); err != nil {
							t.Fatal(err)
						}
						fixture.AssertRawValue(t, b.PG, "TIMESTAMPTZ", ts)
						fixture.AssertRawValue(t, b.PG, "INT2", ver)
						got = append(got, []any{fixture.Normalize(ts), ver})
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					want := [][]any{{extractTime, int64(1)}, {extractTime, int64(7)}, {nil, int64(4)}, {nil, nil}}
					if q[len(q)-1] == '4' {
						want = [][]any{{nil, nil}}
					}
					if q[len(q)-1] == '0' {
						want = nil
					}
					if _, ok := fixture.CompareRows(got, want, false); !ok {
						t.Fatalf("raw rows %#v want %#v", got, want)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					extractMetadata(t, b, columns)
				}()
			}
		}
	})
}

func extractBinds(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	s, err := b.DB.PrepareContext(p.Ctx, extractQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: s, SQL: extractQuery}
	for _, frame := range []struct {
		arg any
		ts  any
		ver any
	}{
		{extractA, extractTime, int64(1)}, {[]byte(extractB), extractTime, int64(7)},
		{nil, nil, nil}, {uuidF, nil, nil}, {extractC, nil, int64(4)},
	} {
		step := extractStep(extractQuery, [][]any{{frame.ts, frame.ver}}, frame.arg)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		extractRunBoth(t, p, b, step)
	}
	for _, frame := range []struct {
		arg  any
		text string
	}{{"bad", "bad"}, {[]byte("bad"), "bad"}, {1, "1"}, {true, "true"}} {
		step := fixture.ImplicitError(extractQuery, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type uuid: "` + frame.text + `"`, Context: "unnamed portal parameter $1 = '...'"}, frame.arg)
		step.SelectRows, step.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, extractStep(extractQuery, [][]any{{extractTime, int64(1)}}, extractA))
	}
}

func TestReferenceUUIDExtractBinds(t *testing.T) {
	extractScenario(t, func(p *fixture.Pair, b fixture.Backend) { extractBinds(t, p, b) })
}

func TestReferenceUUIDExtractCancellation(t *testing.T) {
	extractScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		s, err := b.DB.PrepareContext(p.Ctx, extractQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, e := range []fixture.Executor{b.DB, fixture.HeldStatement{Executor: b.DB, Statement: s, SQL: extractQuery}} {
			rows, err := e.QueryContext(ctx, extractQuery, extractA)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query exposed rows")
			}
			var state interface{ SQLState() string }
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal("cancellation boundary", err)
			}
			fixture.RunStep(t, p.Ctx, e, b.Recorder, extractStep(extractQuery, [][]any{{extractTime, int64(1)}}, extractA))
		}
	})
}
