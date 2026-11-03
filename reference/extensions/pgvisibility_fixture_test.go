package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var pvMapNames = []string{"all_visible", "all_frozen"}
var pvMapTypes = []string{"BOOL", "BOOL"}
var pvPageNames = []string{"all_visible", "all_frozen", "pd_all_visible"}
var pvPageTypes = []string{"BOOL", "BOOL", "BOOL"}
var pvMapRowsNames = []string{"blkno", "all_visible", "all_frozen"}
var pvMapRowsTypes = []string{"INT8", "BOOL", "BOOL"}
var pvPageRowsNames = []string{"blkno", "all_visible", "all_frozen", "pd_all_visible"}
var pvPageRowsTypes = []string{"INT8", "BOOL", "BOOL", "BOOL"}
var pvSummaryTypes = []string{"INT8", "INT8"}

func pvCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "pv_part")
	fixture.SchemaReferenceCleanup(t, "pv_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := pvConn(t, p, b)
			defer c.Close()
			if b.PG {
				clean := func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS pg_visibility CASCADE", "DROP SCHEMA IF EXISTS pv_part CASCADE", "DROP SCHEMA IF EXISTS pv_moved CASCADE"} {
						if _, err := c.ExecContext(ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			for _, q := range []string{"CREATE SCHEMA pv_part", "CREATE EXTENSION pg_visibility SCHEMA pv_part", "CREATE TABLE pv_part.heap(k integer)", "ALTER TABLE pv_part.heap SET(autovacuum_enabled=false)"} {
				pvRun(t, p, b, c, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func pvConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func pvRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func pvBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	pvRun(t, p, b, c, s)
	pvRun(t, p, b, c, fixture.Prepared(s))
}

func pvError(q, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.ControlsReferenceDiagnostic(code, message), args...)
	s.SelectRows = true
	return s
}

func pvEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, target string) {
	t.Helper()
	pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility_map($1::regclass) ORDER BY blkno", pvMapRowsNames, pvMapRowsTypes, nil, target))
	pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility($1::regclass) ORDER BY blkno", pvPageRowsNames, pvPageRowsTypes, nil, target))
	pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility_map_summary($1::regclass)", pvMapNames, pvSummaryTypes, [][]any{{int64(0), int64(0)}}, target))
}

func pvProbe(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, target any, block int64, av, af, pd bool) {
	t.Helper()
	pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility_map($1::regclass,$2::bigint)", pvMapNames, pvMapTypes, [][]any{{av, af}}, target, block))
	pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility($1::regclass,$2::bigint)", pvPageNames, pvPageTypes, [][]any{{av, af, pd}}, target, block))
}

func pvFreeze(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	pvRun(t, p, b, c, fixture.Exec("VACUUM (FREEZE) pv_part.heap", 0))
}

func pvOneFrozen(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	pvProbe(t, p, b, c, "pv_part.heap", 0, true, true, true)
	pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility('pv_part.heap'::regclass) ORDER BY blkno", pvPageRowsNames, pvPageRowsTypes, [][]any{{int64(0), true, true, true}}))
	pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility_map_summary('pv_part.heap'::regclass)", pvMapNames, pvSummaryTypes, [][]any{{int64(1), int64(1)}}))
}

type pvBits struct {
	block      int64
	av, af, pd bool
}

// Physical layout is not parity data. Each backend must independently satisfy
// the released bit implications, contiguous block domain, scalar/SRF agreement,
// and exact summary counts. Callers here have healthy, non-truncated maps;
// that precondition additionally requires native PD=VM. Explicit truncation
// scenarios instead assert the independent false/false/true page contract.
func pvCoherent(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, target string) []pvBits {
	t.Helper()
	q := "SELECT * FROM pv_part.pg_visibility($1::regclass) ORDER BY blkno"
	rows, err := c.QueryContext(p.Ctx, q, target)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, col := range columns {
		if i >= len(pvPageRowsNames) || col.Name() != pvPageRowsNames[i] || col.DatabaseTypeName() != pvPageRowsTypes[i] {
			t.Fatal("page metadata", columns)
		}
	}
	if len(columns) != 4 {
		t.Fatal("page column count", len(columns))
	}
	var bits []pvBits
	var avCount, afCount int64
	for rows.Next() {
		var v pvBits
		if err := rows.Scan(&v.block, &v.av, &v.af, &v.pd); err != nil {
			t.Fatal(err)
		}
		if v.block != int64(len(bits)) || v.af && !v.av || !b.PG && v.pd != v.av {
			t.Fatal("incoherent block", v)
		}
		if v.av {
			avCount++
		}
		if v.af {
			afCount++
		}
		bits = append(bits, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	var mapRows [][]any
	for _, v := range bits {
		mapRows = append(mapRows, []any{v.block, v.av, v.af})
		pvProbe(t, p, b, c, target, v.block, v.av, v.af, v.pd)
	}
	pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility_map($1::regclass) ORDER BY blkno", pvMapRowsNames, pvMapRowsTypes, mapRows, target))
	pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility_map_summary($1::regclass)", pvMapNames, pvSummaryTypes, [][]any{{avCount, afCount}}, target))
	pvProbe(t, p, b, c, target, int64(len(bits))+1, false, false, false)
	return bits
}

func pvNoUniversal(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	bits := pvCoherent(t, p, b, c, "pv_part.heap")
	if len(bits) == 0 {
		t.Fatal("represented private tuple disappeared")
	}
	for _, v := range bits {
		if v.av || v.af || v.pd {
			t.Fatal("unresolved one-page fixture declared universal", v)
		}
	}
}

func pvMetadata(s fixture.Step, pg bool) fixture.Step {
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != len(s.Columns) {
			t.Fatal("metadata width", len(cols), len(s.Columns))
		}
		for i, col := range cols {
			if col.Name() != s.Columns[i] || col.DatabaseTypeName() != s.Types[i] {
				t.Fatal("metadata", i, col.Name(), col.DatabaseTypeName())
			}
			var scan reflect.Type
			switch s.Types[i] {
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			case "INT8":
				scan = reflect.TypeFor[int64]()
			case "RECORD", "TID", "VOID":
				scan = reflect.TypeFor[string]()
				if pg {
					scan = reflect.TypeFor[any]()
				}
			default:
				t.Fatal("unmapped metadata", s.Types[i])
			}
			if col.ScanType() != scan {
				t.Fatal("scan type", i, col.ScanType(), scan)
			}
			if n, known := col.Nullable(); n || known {
				t.Fatal("computed nullability", i, n, known)
			}
			if n, known := col.Length(); n != 0 || known {
				t.Fatal("length", i, n, known)
			}
			if precision, scale, known := col.DecimalSize(); precision != 0 || scale != 0 || known {
				t.Fatal("precision", i, precision, scale, known)
			}
		}
	}
	return s
}

func pvOID(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT $1::regclass::oid", name).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	return oid
}

func pvRollback(c *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
	defer cancel()
	c.ExecContext(ctx, "ROLLBACK")
}

func pvPreparedCleanup(t *testing.T, c *sql.Conn, gid string, active *bool) {
	t.Helper()
	if !*active {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
	defer cancel()
	if _, err := c.ExecContext(ctx, fmt.Sprintf("ROLLBACK PREPARED '%s'", gid)); err != nil {
		t.Error(err)
	}
}
