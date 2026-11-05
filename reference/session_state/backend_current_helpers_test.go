package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Released REL_18_6 pg_proc.dat outputs, independently specified rather than
// copied from pg_stat_activity (whose order and security fields differ).
var backendActivityNames = []string{"datid", "pid", "usesysid", "application_name", "state", "query", "wait_event_type", "wait_event", "xact_start", "query_start", "backend_start", "state_change", "client_addr", "client_hostname", "client_port", "backend_xid", "backend_xmin", "backend_type", "ssl", "sslversion", "sslcipher", "sslbits", "ssl_client_dn", "ssl_client_serial", "ssl_issuer_dn", "gss_auth", "gss_princ", "gss_enc", "gss_delegation", "leader_pid", "query_id"}
var backendActivityTypes = []string{"OID", "INT4", "OID", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TIMESTAMPTZ", "TIMESTAMPTZ", "TIMESTAMPTZ", "TIMESTAMPTZ", "INET", "TEXT", "INT4", "XID", "XID", "TEXT", "BOOL", "TEXT", "TEXT", "INT4", "TEXT", "NUMERIC", "TEXT", "BOOL", "TEXT", "BOOL", "BOOL", "INT4", "INT8"}
var backendActivityOIDs = []uint32{26, 23, 26, 25, 25, 25, 25, 25, 1184, 1184, 1184, 1184, 869, 25, 23, 28, 28, 25, 16, 25, 25, 23, 25, 1700, 25, 16, 25, 16, 16, 23, 20}
var backendWALNames = []string{"wal_records", "wal_fpi", "wal_bytes", "wal_buffers_full", "stats_reset"}
var backendWALTypes = []string{"INT8", "INT8", "NUMERIC", "INT8", "TIMESTAMPTZ"}
var backendWALOIDs = []uint32{20, 20, 1700, 20, 1184}
var backendFunctionNames = []string{"pg_stat_get_activity", "pg_stat_get_backend_io", "pg_stat_get_backend_wal"}
var backendOutputNames = [][]string{backendActivityNames, statIONames, backendWALNames}
var backendOutputTypes = [][]string{backendActivityTypes, statIOTypes, backendWALTypes}

func backendRows(t *testing.T, rows *sql.Rows, pg bool, names, types []string) [][]any {
	t.Helper()
	defer rows.Close()
	got, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(got, names) {
		t.Fatal("fixed OUT names", got, names, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != len(types) {
		t.Fatal("fixed OUT types", columns, err)
	}
	for i, c := range columns {
		if c.DatabaseTypeName() != types[i] {
			t.Fatal("OUT type", i, c.DatabaseTypeName(), types[i])
		}
		if _, known := c.Nullable(); known {
			t.Fatal("computed OUT nullability must be unknown", i)
		}
		if types[i] == "NUMERIC" {
			precision, scale, known := int64(0), int64(0), false
			if pg {
				// lib/pq decodes typmod -1 into this adapter sentinel pair;
				// it is not a declared precision/scale. Wire proof checks -1.
				precision, scale, known = 65535, 65531, true
			}
			if p, s, k := c.DecimalSize(); p != precision || s != scale || k != known {
				t.Fatalf("unconstrained NUMERIC %d: got %d/%d/%t want %d/%d/%t", i, p, s, k, precision, scale, known)
			}
		}
	}
	result := [][]any{}
	for rows.Next() {
		row := make([]any, len(names))
		dst := make([]any, len(names))
		for i := range row {
			dst[i] = &row[i]
		}
		if err := rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		for i, v := range row {
			if data, ok := v.([]byte); ok {
				row[i] = string(data)
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
func backendQuery(t *testing.T, p *fixture.Pair, pg bool, e fixture.Executor, kind int, pid any) [][]any {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, "SELECT * FROM pg_catalog."+backendFunctionNames[kind]+"($1)", pid)
	if err != nil {
		t.Fatal(err)
	}
	return backendRows(t, rows, pg, backendOutputNames[kind], backendOutputTypes[kind])
}
func backendCount(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := c.QueryRowContext(p.Ctx, q, args...).Scan(&n); err != nil {
		t.Fatal(q, err)
	}
	return n
}
func backendIOCheck(t *testing.T, rows [][]any, pg bool) {
	t.Helper()
	if len(rows) != 8 {
		t.Fatal("eight actual client capabilities", len(rows))
	}
	want := statIOExpected()
	seen := map[statIOKey]bool{}
	for _, row := range rows {
		key := statIOKey{fmt.Sprint(row[0]), fmt.Sprint(row[1]), fmt.Sprint(row[2])}
		mask, ok := want[key]
		if !ok || key.backend != "client backend" || seen[key] {
			t.Fatal("client capability bag", key)
		}
		seen[key] = true
		for j, op := range "rrrwwwbbeeehvuff" {
			i := j + 3
			if !strings.ContainsRune(mask, op) {
				if row[i] != nil {
					t.Fatal("unsupported field", key, statIONames[i], row[i])
				}
				continue
			}
			var n float64
			switch statIOTypes[i] {
			case "INT8":
				v, ok := row[i].(int64)
				if !ok {
					t.Fatal("INT8", row[i])
				}
				n = float64(v)
			case "FLOAT8":
				v, ok := row[i].(float64)
				if !ok {
					t.Fatal("FLOAT8", row[i])
				}
				n = v
			case "NUMERIC":
				v, ok := row[i].(string)
				if !ok {
					t.Fatal("NUMERIC", row[i])
				}
				var err error
				n, err = strconv.ParseFloat(v, 64)
				if err != nil || strings.ContainsAny(v, ".-+eE") {
					t.Fatal("exact nonnegative integral bytes", v, err)
				}
			}
			if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || !pg && n != 0 {
				t.Fatal("physical event estimate", key, i, n)
			}
		}
		if !reflect.DeepEqual(rows[0][19], row[19]) {
			t.Fatal("one backend reset cut", row)
		}
	}
}
func backendWALCheck(t *testing.T, rows [][]any, pg bool, present bool) {
	t.Helper()
	if len(rows) != 1 {
		t.Fatal("scalar record FROM cardinality", rows)
	}
	for i, v := range rows[0] {
		if !present {
			if v != nil {
				t.Fatal("whole NULL expands NULL fields", rows)
			}
			continue
		}
		if i == 4 {
			if v != nil {
				if stamp, ok := v.(time.Time); !ok || stamp.IsZero() {
					t.Fatal("reset timestamp", v)
				}
			}
			continue
		}
		text := fmt.Sprint(v)
		if v == nil || strings.ContainsAny(text, ".-+eE") {
			t.Fatal("present exact counter", v)
		}
		n, err := strconv.ParseUint(text, 10, 64)
		if err != nil || !pg && n != 0 {
			t.Fatal("physical WAL estimate", v, err)
		}
	}
}
func backendStamp(t *testing.T, p *fixture.Pair, pg bool, c *sql.Conn, pid int64, io bool) any {
	t.Helper()
	if io {
		rows := backendQuery(t, p, pg, c, 1, pid)
		if len(rows) != 8 {
			t.Fatal(rows)
		}
		return rows[0][19]
	}
	rows := backendQuery(t, p, pg, c, 2, pid)
	return rows[0][4]
}
func backendReset(t *testing.T, p *fixture.Pair, admin *sql.Conn, pid int64) (time.Time, time.Time) {
	t.Helper()
	var before, after time.Time
	if err := admin.QueryRowContext(p.Ctx, "SELECT clock_timestamp()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	activityExec(t, p, admin, fmt.Sprintf("SELECT pg_stat_reset_backend_stats(%d)", pid))
	if err := admin.QueryRowContext(p.Ctx, "SELECT clock_timestamp()").Scan(&after); err != nil {
		t.Fatal(err)
	}
	return before, after
}
func backendBound(t *testing.T, value any, before, after time.Time) time.Time {
	t.Helper()
	stamp, ok := value.(time.Time)
	if !ok || stamp.Before(before) || stamp.After(after) {
		t.Fatal("bounded genuine backend reset", value, before, after)
	}
	return stamp
}
