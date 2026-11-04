package reference_test

import (
	"database/sql"
	"reflect"
	"sort"
	"testing"

	"stuntgres/reference/fixture"
)

var sslNames = []string{"pid", "ssl", "version", "cipher", "bits", "client_dn", "client_serial", "issuer_dn"}
var sslTypes = []string{"INT4", "BOOL", "TEXT", "TEXT", "INT4", "TEXT", "NUMERIC", "TEXT"}

const sslSQL = "SELECT * FROM pg_catalog.pg_stat_ssl WHERE pid=$1"
const sslPairSQL = "SELECT * FROM pg_catalog.pg_stat_ssl WHERE pid IN ($1,$2) ORDER BY pid"

func sslMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, sslNames) {
		t.Fatal("SSL column names", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(sslTypes) {
		t.Fatal("SSL column types", types, err)
	}
	for i, typ := range types {
		if typ.DatabaseTypeName() != sslTypes[i] {
			t.Fatal("SSL declared type", names[i], typ.DatabaseTypeName())
		}
		if !pg {
			if nullable, known := typ.Nullable(); !nullable || !known {
				t.Fatal("SSL nullable declaration", names[i], nullable, known)
			}
			want := reflect.TypeOf("")
			if i == 0 || i == 4 {
				want = reflect.TypeOf(int64(0))
			}
			if i == 1 {
				want = reflect.TypeOf(false)
			}
			if typ.ScanType() != want {
				t.Fatal("SSL scan type", names[i], typ.ScanType(), want)
			}
			if i == 6 {
				if precision, scale, ok := typ.DecimalSize(); ok {
					t.Fatal("unconstrained numeric precision", precision, scale)
				}
			}
		}
	}
}

func sslExpected(pids ...int64) [][]any {
	pids = append([]int64(nil), pids...)
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	rows := make([][]any, 0, len(pids))
	for _, pid := range pids {
		rows = append(rows, []any{pid, false, nil, nil, nil, nil, nil, nil})
	}
	return rows
}

func sslStep(query string, pg bool, pids []int64, args ...any) fixture.Step {
	step := fixture.Select(query, sslNames, sslTypes, sslExpected(pids...), args...)
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { sslMetadata(t, rows, pg) }
	return step
}

func sslWant(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, pid int64, present bool) {
	t.Helper()
	var pids []int64
	if present {
		pids = []int64{pid}
	}
	fixture.RunStep(t, p.Ctx, executor, b.Recorder, sslStep(sslSQL, b.PG, pids, pid))
}

func sslPair(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, a, other int64) {
	t.Helper()
	if a <= 0 || other <= 0 || a == other {
		t.Fatal("distinct positive physical identities", a, other)
	}
	fixture.RunStep(t, p.Ctx, executor, b.Recorder, sslStep(sslPairSQL, b.PG, []int64{a, other}, a, other))
}

func sslHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	statement, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, err)
	}
	return fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
}

func sslFrozen(t *testing.T, rows *sql.Rows, pg bool, want ...int64) [][]any {
	t.Helper()
	defer rows.Close()
	sslMetadata(t, rows, pg)
	var got [][]any
	for rows.Next() {
		row := make([]any, len(sslNames))
		dest := make([]any, len(row))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil || !reflect.DeepEqual(got, sslExpected(want...)) {
		t.Fatal("detached SSL rows", got, want, err)
	}
	return got
}

func sslSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM ssl_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
}
