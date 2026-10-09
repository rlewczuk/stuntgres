package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

var datetimeCurrentNames = []string{"current_date", "current_time", "current_timestamp", "localtime", "localtimestamp"}
var datetimeCurrentTypes = []string{"DATE", "TIMETZ", "TIMESTAMPTZ", "TIME", "TIMESTAMP"}

const datetimeCurrentProjection = "CURRENT_DATE,CURRENT_TIME,CURRENT_TIMESTAMP,LOCALTIME,LOCALTIMESTAMP"

// Independent oracle: the prerequisite relative literal witnesses the transaction
// clock, never a CURRENT expression or a production rounding/zone helper.
func datetimeCurrentWitness(t *testing.T, ctx context.Context, x referenceExecutor) time.Time {
	t.Helper()
	rows, err := x.QueryContext(ctx, "SELECT TIMESTAMPTZ 'now' FROM dc_anchor WHERE n=1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing prerequisite clock witness", rows.Err())
	}
	var stamp time.Time
	if err := rows.Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("clock witness cardinality", rows.Err())
	}
	return stamp.UTC()
}

func datetimeCurrentExec(t *testing.T, ctx context.Context, x referenceExecutor, q string, count int64) {
	t.Helper()
	result, err := x.ExecContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	got, err := result.RowsAffected()
	if err != nil || got != count {
		t.Fatalf("%s affected=%d error=%v want=%d", q, got, err, count)
	}
}

func datetimeCurrentSeed(t *testing.T, ctx context.Context, x referenceExecutor) {
	t.Helper()
	datetimeCurrentExec(t, ctx, x, "SET TIME ZONE 'UTC'", 0)
	datetimeCurrentExec(t, ctx, x, "SET DateStyle='ISO,MDY'", 0)
	datetimeCurrentExec(t, ctx, x, "CREATE TABLE dc_anchor(n INTEGER)", 0)
	datetimeCurrentExec(t, ctx, x, "INSERT INTO dc_anchor VALUES(1),(2)", 2)
}

func datetimeCurrentMetadata(t *testing.T, pg bool, meta []*sql.ColumnType, names, types []string, storedColumns ...bool) {
	t.Helper()
	if len(meta) != len(types) {
		t.Fatalf("metadata=%#v want types=%v", meta, types)
	}
	wantNullable := !pg && len(storedColumns) > 0 && storedColumns[0]
	for i, c := range meta {
		scan := reflect.TypeFor[time.Time]()
		if !pg && (types[i] == "DATE" || types[i] == "TIME" || types[i] == "TIMETZ") {
			scan = reflect.TypeFor[string]()
		}
		length, lk := c.Length()
		precision, scale, dk := c.DecimalSize()
		nullable, nk := c.Nullable()
		if c.Name() != names[i] || c.DatabaseTypeName() != types[i] || c.ScanType() != scan || lk || dk || nk != wantNullable || length != 0 || precision != 0 || scale != 0 || nullable != wantNullable {
			t.Errorf("metadata[%d]: name=%q type=%q scan=%v length=%d/%v decimal=%d,%d/%v nullable=%v/%v; want name=%q type=%q scan=%v nullable=%v/%v unavailable length/decimal", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lk, precision, scale, dk, nullable, nk, names[i], types[i], scan, wantNullable, wantNullable)
		}
	}
}

func datetimeCurrentRound(us int64, p int) int64 {
	quantum := int64(1)
	for i := p; i < 6; i++ {
		quantum *= 10
	}
	if us < 0 {
		return -((-us + quantum/2) / quantum) * quantum
	}
	return ((us + quantum/2) / quantum) * quantum
}

func datetimeCurrentClock(us int64) string {
	seconds := us / 1000000
	s := fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	if fraction := us % 1000000; fraction != 0 {
		s += "." + strings.TrimRight(fmt.Sprintf("%06d", fraction), "0")
	}
	return s
}

func datetimeCurrentExpected(stamp time.Time, east, p int) []string {
	zone := time.FixedZone("oracle", east)
	local := stamp.In(zone)
	civil := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.UTC)
	epoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	rounded := func(v time.Time) time.Time {
		return epoch.Add(time.Duration(datetimeCurrentRound(v.Sub(epoch).Microseconds(), p)) * time.Microsecond)
	}
	clock := datetimeCurrentClock(datetimeCurrentRound(int64(local.Hour()*3600+local.Minute()*60+local.Second())*1000000+int64(local.Nanosecond()/1000), p))
	sign := "+"
	offset := east
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	suffix := fmt.Sprintf("%s%02d", sign, offset/3600)
	if offset%3600 != 0 {
		suffix += fmt.Sprintf(":%02d", offset/60%60)
	}
	format := func(v time.Time) string {
		return v.Format("2006-01-02 ") + datetimeCurrentClock(int64(v.Hour()*3600+v.Minute()*60+v.Second())*1000000+int64(v.Nanosecond()/1000))
	}
	return []string{local.Format("2006-01-02"), clock + suffix, format(rounded(stamp).In(zone)) + suffix, clock, format(rounded(civil))}
}

// Text output supplies an independently calculated exact value oracle. The raw
// temporal query separately checks driver codecs, actual descriptors and labels.
func datetimeCurrentCheck(t *testing.T, ctx context.Context, x referenceExecutor, pg bool, q string, stamp time.Time, east, p, count int, storedColumns ...bool) {
	t.Helper()
	datetimeCurrentCheckPrecisions(t, ctx, x, pg, q, stamp, east, count, [5]int{p, p, p, p, p}, storedColumns...)
}

func datetimeCurrentCheckPrecisions(t *testing.T, ctx context.Context, x referenceExecutor, pg bool, q string, stamp time.Time, east, count int, precisions [5]int, storedColumns ...bool) {
	t.Helper()
	rows, err := x.QueryContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	datetimeCurrentMetadata(t, pg, meta, datetimeCurrentNames, datetimeCurrentTypes, storedColumns...)
	n := 0
	for rows.Next() {
		raw := make([]any, 5)
		dest := make([]any, 5)
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, v := range raw {
			p := precisions[i]
			expected := datetimeCurrentExpected(stamp, east, p)
			if !pg && (i == 0 || i == 1 || i == 3) {
				if s, ok := v.(string); !ok || s != expected[i] {
					t.Errorf("%s raw[%d]=%T %#v want %q", q, i, v, v, expected[i])
				}
			} else {
				value, ok := v.(time.Time)
				if !ok {
					t.Errorf("%s raw[%d]=%T %#v want time.Time", q, i, v, v)
					continue
				}
				switch i {
				case 0:
					if value.Format("2006-01-02") != expected[i] {
						t.Errorf("raw DATE=%v want=%s", value, expected[i])
					}
				case 1, 3:
					clock := datetimeCurrentClock(int64(value.Hour()*3600+value.Minute()*60+value.Second())*1000000 + int64(value.Nanosecond()/1000))
					want := expected[3]
					_, off := value.Zone()
					if clock != want || (i == 1 && off != east) || (i == 3 && off != 0) {
						t.Errorf("raw clock=%v offset=%d want=%s offset=%d", value, off, want, east)
					}
				case 2:
					epoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
					want := epoch.Add(time.Duration(datetimeCurrentRound(stamp.Sub(epoch).Microseconds(), p)) * time.Microsecond)
					_, off := value.Zone()
					if !value.Equal(want) || off != east {
						t.Errorf("raw TIMESTAMPTZ=%v offset=%d want=%v offset=%d", value, off, want, east)
					}
				case 4:
					_, off := value.Zone()
					if value.Format("2006-01-02 ")+datetimeCurrentClock(int64(value.Hour()*3600+value.Minute()*60+value.Second())*1000000+int64(value.Nanosecond()/1000)) != expected[i] || off != 0 {
						t.Errorf("raw TIMESTAMP=%v want=%s civil UTC", value, expected[i])
					}
				}
			}
		}
		n++
	}
	if rows.Err() != nil || n != count {
		t.Fatalf("%s rows=%d error=%v want=%d", q, n, rows.Err(), count)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	datetimeCurrentMetadata(t, pg, meta, datetimeCurrentNames, datetimeCurrentTypes, storedColumns...)
}

func datetimeCurrentTextCheck(t *testing.T, ctx context.Context, x referenceExecutor, record *referenceRecorder, stamp time.Time, east, p int) {
	t.Helper()
	forms := []string{"CURRENT_DATE", "CURRENT_TIME", "CURRENT_TIMESTAMP", "LOCALTIME", "LOCALTIMESTAMP"}
	for i := 1; i < 5; i++ {
		forms[i] += "(" + strconv.Itoa(p) + ")"
	}
	for i := range forms {
		forms[i] += "::TEXT AS " + datetimeCurrentNames[i]
	}
	expected := datetimeCurrentExpected(stamp, east, p)
	row := make([]any, 5)
	for i := range row {
		row[i] = expected[i]
	}
	referenceRunStep(t, ctx, x, record, referenceSelect("SELECT "+strings.Join(forms, ",")+" FROM dc_anchor WHERE n=1", datetimeCurrentNames, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{row}))
}

func TestReferenceDatetimeCurrentFormsMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			datetimeCurrentSeed(t, p.ctx, conn)
			datetimeCurrentExec(t, p.ctx, conn, "BEGIN", 0)
			defer conn.ExecContext(p.ctx, "ROLLBACK")
			stamp := datetimeCurrentWitness(t, p.ctx, conn)
			for _, tail := range []string{"", " FROM dc_anchor ORDER BY n", " LIMIT 0"} {
				count := 1
				if tail == " FROM dc_anchor ORDER BY n" {
					count = 2
				}
				if tail == " LIMIT 0" {
					count = 0
				}
				datetimeCurrentCheck(t, p.ctx, conn, backend.pg, "SELECT "+datetimeCurrentProjection+tail, stamp, 0, 6, count)
			}
			q := "SELECT " + datetimeCurrentProjection
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
			for range 2 {
				datetimeCurrentCheck(t, p.ctx, held, backend.pg, q, stamp, 0, 6, 1)
			}
			aliasSQL := "SELECT CURRENT_DATE AS d,CURRENT_TIME(3) AS t,CURRENT_TIMESTAMP(3) AS z,LOCALTIME(3) AS l,LOCALTIMESTAMP(3) AS s LIMIT 0"
			rows, err := conn.QueryContext(p.ctx, aliasSQL)
			if err != nil {
				t.Fatal(err)
			}
			meta, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			datetimeCurrentMetadata(t, backend.pg, meta, []string{"d", "t", "z", "l", "s"}, datetimeCurrentTypes)
			if rows.Next() || rows.Err() != nil {
				t.Fatal("LIMIT 0", rows.Err())
			}
			rows.Close()
			datetimeCurrentMetadata(t, backend.pg, meta, []string{"d", "t", "z", "l", "s"}, datetimeCurrentTypes)
			// DDL must precede explicit transactions on the bounded native dialect.
			datetimeCurrentExec(t, p.ctx, conn, "COMMIT", 0)
			datetimeCurrentExec(t, p.ctx, conn, "CREATE TABLE dc_values(d DATE,t TIMETZ,z TIMESTAMPTZ,l TIME,s TIMESTAMP)", 0)
			datetimeCurrentExec(t, p.ctx, conn, "BEGIN", 0)
			stamp = datetimeCurrentWitness(t, p.ctx, conn)
			datetimeCurrentExec(t, p.ctx, conn, "INSERT INTO dc_values SELECT "+datetimeCurrentProjection+" FROM dc_anchor WHERE n=1", 1)
			datetimeCurrentCheck(t, p.ctx, conn, backend.pg, "SELECT d AS current_date,t AS current_time,z AS current_timestamp,l AS localtime,s AS localtimestamp FROM dc_values", stamp, 0, 6, 1, true)
			datetimeCurrentExec(t, p.ctx, conn, "INSERT INTO dc_values VALUES(CURRENT_DATE,CURRENT_TIME,CURRENT_TIMESTAMP,LOCALTIME,LOCALTIMESTAMP)", 1)
			datetimeCurrentCheck(t, p.ctx, conn, backend.pg, "SELECT d AS current_date,t AS current_time,z AS current_timestamp,l AS localtime,s AS localtimestamp FROM dc_values", stamp, 0, 6, 2, true)
		})
	}
}

func TestReferenceDatetimeCurrentPrecision(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			datetimeCurrentSeed(t, p.ctx, conn)
			datetimeCurrentExec(t, p.ctx, conn, "BEGIN", 0)
			defer conn.ExecContext(p.ctx, "ROLLBACK")
			stamp := datetimeCurrentWitness(t, p.ctx, conn)
			for precision := range 7 {
				forms := []string{"CURRENT_DATE", "CURRENT_TIME", "CURRENT_TIMESTAMP", "LOCALTIME", "LOCALTIMESTAMP"}
				for i := 1; i < 5; i++ {
					forms[i] += "(" + strconv.Itoa(precision) + ")"
				}
				q := "SELECT " + strings.Join(forms, ",")
				datetimeCurrentCheck(t, p.ctx, conn, backend.pg, q, stamp, 0, precision, 1)
				stmt, err := conn.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				datetimeCurrentCheck(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}, backend.pg, q, stamp, 0, precision, 1)
				stmt.Close()
				datetimeCurrentTextCheck(t, p.ctx, conn, backend.recorder, stamp, 0, precision)
			}
			datetimeCurrentCheck(t, p.ctx, conn, backend.pg, "SELECT "+datetimeCurrentProjection, stamp, 0, 6, 1)
		})
	}
}
