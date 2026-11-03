package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

var xactBlockNames = []string{"pg_stat_get_xact_blocks_fetched", "pg_stat_get_xact_blocks_hit"}

const xactBlockSQL = "SELECT pg_catalog.pg_stat_get_xact_blocks_fetched($1::oid),pg_catalog.pg_stat_get_xact_blocks_hit($1::oid)"

type xactBlockPair struct{ fetched, hit int64 }

func xactBlockMetadata(t *testing.T, rows *sql.Rows, names ...string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != len(names) {
		t.Fatal("block metadata", columns, err)
	}
	for i, column := range columns {
		if column.Name() != names[i] || column.DatabaseTypeName() != "INT8" || column.ScanType() != reflect.TypeFor[int64]() {
			t.Fatalf("block column %d: %s %s %v", i, column.Name(), column.DatabaseTypeName(), column.ScanType())
		}
		if _, known := column.Nullable(); known {
			t.Fatal("computed block result claimed known nullability")
		}
	}
}

func xactBlockRead(t *testing.T, p *fixture.Pair, c *sql.Conn, stmt *sql.Stmt, oid int64) xactBlockPair {
	t.Helper()
	var rows *sql.Rows
	var err error
	if stmt == nil {
		rows, err = c.QueryContext(p.Ctx, xactBlockSQL, oid)
	} else {
		rows, err = stmt.QueryContext(p.Ctx, oid)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	xactBlockMetadata(t, rows, xactBlockNames...)
	var got xactBlockPair
	if !rows.Next() {
		t.Fatal("missing block result", rows.Err())
	}
	if err := rows.Scan(&got.fetched, &got.hit); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil || rows.NextResultSet() {
		t.Fatal("block result cardinality", rows.Err())
	}
	if got.hit < 0 || got.fetched < got.hit {
		t.Fatalf("fetched must include hits: %+v", got)
	}
	return got
}

func xactBlockWant(t *testing.T, got, want xactBlockPair) {
	t.Helper()
	if got != want {
		t.Fatalf("pending blocks %+v want %+v", got, want)
	}
}

func xactBlockAdvanced(t *testing.T, b fixture.Backend, before, after xactBlockPair) {
	t.Helper()
	if after.fetched <= before.fetched || after.hit < before.hit || after.hit > after.fetched {
		t.Fatalf("performed read did not advance valid pair: %+v -> %+v", before, after)
	}
	// Native models one resident logical-page request per reached invocation,
	// not PostgreSQL's physical buffer request count.
	if !b.PG && (after.fetched-before.fetched != 1 || after.hit-before.hit != 1) {
		t.Fatalf("native request delta %+v -> %+v, want fetched+1/hit+1", before, after)
	}
}

func xactBlockOID(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT $1::regclass::oid::bigint", name).Scan(&oid); err != nil || oid <= 0 {
		t.Fatal("relation identity", name, oid, err)
	}
	return oid
}

func xactBlockRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, values ...int64) {
	t.Helper()
	var rows [][]any
	for _, value := range values {
		rows = append(rows, []any{value})
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(query, []string{"id"}, []string{"INT4"}, rows))
}

func xactBlockBegin(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	statsExec(t, p, b, c, "BEGIN", 0)
}

func xactBlockTerminal(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, terminal string) {
	t.Helper()
	if b.PG {
		// Fixture synchronization only: do not add a native force-flush builtin.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_stat_force_next_flush()", []string{"pg_stat_force_next_flush"}, []string{"VOID"}, [][]any{{""}}))
	}
	statsExec(t, p, b, c, terminal, 0)
}

func TestReferenceStatsXactBlocksSignatures(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		setup := statActivityConn(t, p, b)
		statsCreate(t, p, b, setup, "xact_signature")
		statsExec(t, p, b, setup, "INSERT INTO xact_signature VALUES(7)", 1)
		oid := xactBlockOID(t, p, c, "xact_signature")
		const absent = int64(4294967295)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE oid=4294967295::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		xactBlockBegin(t, p, b, c)
		defer c.ExecContext(context.Background(), "ROLLBACK")
		for _, name := range xactBlockNames {
			for _, qualification := range []string{"", "pg_catalog."} {
				for _, prepared := range []bool{false, true} {
					for _, tc := range []struct {
						argument string
						value    any
					}{{"NULL", nil}, {"NULL::oid", nil}, {"0::oid", int64(0)}, {"4294967295::oid", int64(0)}, {strconv.FormatInt(oid, 10) + "::oid", int64(0)}} {
						step := fixture.Select("SELECT "+qualification+name+"("+tc.argument+")", []string{name}, []string{"INT8"}, [][]any{{tc.value}})
						step.Prepared = prepared
						step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { xactBlockMetadata(t, rows, name) }
						fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					}
					for _, argument := range []string{"$1", "$1::oid"} {
						for _, value := range []any{nil, int64(0), oid, absent} {
							var want any = int64(0)
							if value == nil {
								want = nil
							}
							step := fixture.Select("SELECT "+qualification+name+"("+argument+")", []string{name}, []string{"INT8"}, [][]any{{want}}, value)
							step.Prepared = prepared
							step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { xactBlockMetadata(t, rows, name) }
							fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
						}
					}
				}
			}
		}
		query := `SELECT oid::bigint AS oid,proname::text AS name,pronamespace::bigint AS namespace,pronargs::bigint AS nargs,prorettype::bigint AS result,proisstrict AS strict,provolatile::text AS volatility,proparallel::text AS parallel,proretset AS is_set,prosrc AS source,prolang::bigint AS language,proargtypes::text AS inputs,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names,pronargdefaults::bigint AS defaults,provariadic::bigint AS variadic FROM pg_proc WHERE proname IN ('pg_stat_get_xact_blocks_fetched','pg_stat_get_xact_blocks_hit') ORDER BY oid`
		var expected [][]any
		for i, name := range xactBlockNames {
			expected = append(expected, []any{int64(3044 + i), name, int64(11), int64(1), int64(20), true, "v", "r", false, name, int64(12), "26", nil, nil, nil, int64(0), int64(0)})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(query, []string{"oid", "name", "namespace", "nargs", "result", "strict", "volatility", "parallel", "is_set", "source", "language", "inputs", "alltypes", "modes", "names", "defaults", "variadic"}, []string{"INT8", "TEXT", "INT8", "INT8", "INT8", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "INT8", "INT8"}, expected))
		// A PID-shaped number remains an OID, never another backend's address.
		pid := statActivityPID(t, p, setup)
		var matchingRelations int64
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_class WHERE oid=$1::oid", pid).Scan(&matchingRelations); err != nil {
			t.Fatal(err)
		}
		pidValue := xactBlockRead(t, p, c, nil, pid)
		if matchingRelations == 0 {
			xactBlockWant(t, pidValue, xactBlockPair{})
		}
		statsExec(t, p, b, c, "ROLLBACK", 0)
	})
}
