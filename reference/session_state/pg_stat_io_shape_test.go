package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var statIONames = []string{"backend_type", "object", "context", "reads", "read_bytes", "read_time", "writes", "write_bytes", "write_time", "writebacks", "writeback_time", "extends", "extend_bytes", "extend_time", "hits", "evictions", "reuses", "fsyncs", "fsync_time", "stats_reset"}
var statIOTypes = []string{"TEXT", "TEXT", "TEXT", "INT8", "NUMERIC", "FLOAT8", "INT8", "NUMERIC", "FLOAT8", "INT8", "FLOAT8", "INT8", "NUMERIC", "FLOAT8", "INT8", "INT8", "INT8", "INT8", "FLOAT8", "TIMESTAMPTZ"}
var statIOOIDs = []int64{25, 25, 25, 20, 1700, 701, 20, 1700, 701, 20, 701, 20, 1700, 701, 20, 20, 20, 20, 701, 1184}

const statIOSelect = "SELECT * FROM pg_catalog.pg_stat_io ORDER BY backend_type,object,context"

type statIOKey struct{ backend, object, context string }
type statIORow [20]any

// Independently transcribed released capability expectations. Each literal mask
// lists supported operations: read/write/writeback/extend/hit/evict/reuse/fsync.
// This is a table of outcomes, not the production exclusion algorithm.
func statIOExpected() map[statIOKey]string {
	got := make(map[statIOKey]string)
	for _, group := range []struct{ backends, relations, temp, wal string }{
		{"autovacuum launcher", "bulkread:rwbhuv init:rwbhvf normal:rwbhvf", "", "wf"},
		{"autovacuum worker", "bulkread:rwbhuv init:rwbehvf normal:rwbehvf vacuum:rwbehuv", "", "wf"},
		{"background writer", "init:wbf normal:wbf", "", "wf"},
		{"checkpointer", "init:wbf normal:wbf", "", "rwf"},
		{"background worker|client backend|io worker|slotsync worker|walsender", "bulkread:rwbhuv bulkwrite:rwbehuv init:rwbehvf normal:rwbehvf vacuum:rwbehuv", "rwehv", "rwf"},
		{"standalone backend|startup", "bulkread:rwbhuv bulkwrite:rwbehuv init:rwbehvf normal:rwbehvf vacuum:rwbehuv", "", "rwf"},
		{"walreceiver|walwriter", "", "", "wf"},
		{"walsummarizer", "", "", "rwf"},
	} {
		for _, backend := range strings.Split(group.backends, "|") {
			for _, relation := range strings.Fields(group.relations) {
				parts := strings.Split(relation, ":")
				got[statIOKey{backend, "relation", parts[0]}] = parts[1]
			}
			if group.temp != "" {
				got[statIOKey{backend, "temp relation", "normal"}] = group.temp
			}
			got[statIOKey{backend, "wal", "init"}] = "wf"
			got[statIOKey{backend, "wal", "normal"}] = group.wal
		}
	}
	return got
}

func statIOMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, statIONames) {
		t.Fatal("exact twenty names", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 20 {
		t.Fatal("twenty types", types, err)
	}
	for i, typ := range types {
		if typ.Name() != statIONames[i] || typ.DatabaseTypeName() != statIOTypes[i] {
			t.Fatalf("column %d: %s/%s", i, typ.Name(), typ.DatabaseTypeName())
		}
		if nullable, known := typ.Nullable(); nullable != !pg || known != !pg {
			t.Fatalf("nullable %d: %v/%v", i, nullable, known)
		}
		if !pg {
			want := reflect.TypeFor[int64]()
			switch statIOTypes[i] {
			case "TEXT", "NUMERIC":
				want = reflect.TypeFor[string]()
			case "FLOAT8":
				want = reflect.TypeFor[float64]()
			case "TIMESTAMPTZ":
				want = reflect.TypeFor[time.Time]()
			}
			if typ.ScanType() != want {
				t.Fatalf("ScanType %d: %v want %v", i, typ.ScanType(), want)
			}
			wantLength, wantLengthKnown := int64(0), false
			if statIOTypes[i] == "TEXT" {
				wantLength, wantLengthKnown = math.MaxInt64, true
			}
			if length, known := typ.Length(); length != wantLength || known != wantLengthKnown {
				t.Fatalf("Length %d: %d/%v want %d/%v", i, length, known, wantLength, wantLengthKnown)
			}
			if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
				t.Fatal("unconstrained numeric precision", i, precision, scale, known)
			}
		}
	}
}

func statIORead(t *testing.T, rows *sql.Rows, pg bool) []statIORow {
	t.Helper()
	defer rows.Close()
	statIOMetadata(t, rows, pg)
	result := []statIORow{}
	for rows.Next() {
		var row statIORow
		var dest [20]any
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest[:]...); err != nil {
			t.Fatal(err)
		}
		// pq returns text/numeric as bytes; normalize only their public carrier,
		// never learn expected masks, counts or metadata from either backend.
		for i, v := range row {
			if bytes, ok := v.([]byte); ok {
				row[i] = string(bytes)
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func statIOQuery(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, args ...any) []statIORow {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, query, args...)
	if err != nil {
		t.Fatal(query, err)
	}
	return statIORead(t, rows, b.PG)
}

func statIOCheck(t *testing.T, rows []statIORow, pg bool) {
	t.Helper()
	want := statIOExpected()
	if len(want) != 79 || len(rows) != 79 {
		t.Fatal("79 static capabilities", len(want), len(rows))
	}
	seen := map[statIOKey]bool{}
	totals := map[string]int{}
	for _, row := range rows {
		key := statIOKey{fmt.Sprint(row[0]), fmt.Sprint(row[1]), fmt.Sprint(row[2])}
		mask, ok := want[key]
		if !ok || seen[key] {
			t.Fatal("unexpected/duplicate capability", key)
		}
		seen[key] = true
		totals[key.backend]++
		for j, operation := range "rrrwwwbbeeehvuff" {
			i := j+3
			if !strings.ContainsRune(mask, operation) {
				if row[i] != nil {
					t.Fatalf("unsupported %v %s: %#v", key, statIONames[i], row[i])
				}
				continue
			}
			var number float64
			switch statIOTypes[i] {
			case "INT8":
				v, ok := row[i].(int64)
				if !ok { t.Fatalf("typed bigint %v %s: %#v", key, statIONames[i], row[i]) }
				number = float64(v)
			case "FLOAT8":
				v, ok := row[i].(float64)
				if !ok { t.Fatalf("typed float8 %v %s: %#v", key, statIONames[i], row[i]) }
				number = v
			case "NUMERIC":
				v, ok := row[i].(string)
				if !ok { t.Fatalf("typed numeric %v %s: %#v", key, statIONames[i], row[i]) }
				var err error
				number, err = strconv.ParseFloat(v, 64)
				if err != nil || !pg && v != "0" { t.Fatal("exact numeric zero/nonnegative physical bytes", key, v, err) }
			}
			if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || !pg && number != 0 {
				t.Fatalf("absent/native or physical nonnegative %v %s: %v", key, statIONames[i], number)
			}
		}
		if !reflect.DeepEqual(row[19], rows[0][19]) {
			t.Fatal("one IO-family reset timestamp", key, row[19], rows[0][19])
		}
		if row[19] != nil {
			stamp, ok := row[19].(time.Time)
			if !ok || stamp.IsZero() || stamp.Nanosecond()%1000 != 0 { t.Fatal("typed reset timestamp", row[19]) }
		}
	}
	for backend, count := range map[string]int{"autovacuum launcher":5, "autovacuum worker":6, "background worker":8, "background writer":4, "checkpointer":4, "client backend":8, "io worker":8, "slotsync worker":8, "standalone backend":7, "startup":7, "walreceiver":2, "walsender":8, "walsummarizer":2, "walwriter":2} {
		if totals[backend] != count { t.Fatal("backend total", backend, totals[backend], count) }
	}
}

func TestReferencePGStatIOShape(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{statIOSelect, "SELECT * FROM pg_stat_io ORDER BY backend_type,object,context", "SELECT * FROM pg_catalog.pg_stat_io WHERE false"} {
			for _, prepared := range []bool{false, true} {
				var e fixture.Executor = c
				if prepared {
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil { t.Fatal(err) }
					defer stmt.Close()
					e = fixture.HeldStatement{Executor:c, Statement:stmt, SQL:query}
				}
				got := statIOQuery(t, p, b, e, query)
				if strings.HasSuffix(query, "false") {
					if len(got) != 0 { t.Fatal("empty metadata query", got) }
				} else { statIOCheck(t, got, b.PG) }
			}
		}
		for _, setting := range []string{"on", "off"} {
			activityExec(t, p, c, "SET track_io_timing='"+setting+"'")
			activityExec(t, p, c, "SET track_wal_io_timing='"+setting+"'")
			statIOCheck(t, statIOQuery(t, p, b, c, statIOSelect), b.PG)
		}
		attrs := make([][]any, 20)
		for i, name := range statIONames { attrs[i] = []any{int64(i+1), name, statIOOIDs[i], false, int64(-1), int64(0)} }
		step := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attndims FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_io'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "INT2"}, attrs)
		fixture.PgDescriptionExecutorRows(t, p, b, c, step)
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		step = fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_io'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(20), true, true, true}})
		fixture.PgDescriptionExecutorRows(t, p, b, c, step)
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		if !b.PG {
			step = fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_io'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12301), int64(12303), int64(12302)}})
			fixture.PgDescriptionExecutorRows(t, p, b, c, step)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		}
		definition := ` SELECT backend_type,
    object,
    context,
    reads,
    read_bytes,
    read_time,
    writes,
    write_bytes,
    write_time,
    writebacks,
    writeback_time,
    extends,
    extend_bytes,
    extend_time,
    hits,
    evictions,
    reuses,
    fsyncs,
    fsync_time,
    stats_reset
   FROM pg_stat_get_io() b(backend_type, object, context, reads, read_bytes, read_time, writes, write_bytes, write_time, writebacks, writeback_time, extends, extend_bytes, extend_time, hits, evictions, reuses, fsyncs, fsync_time, stats_reset);`
		step = fixture.Select("SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_io'", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
	})
}
