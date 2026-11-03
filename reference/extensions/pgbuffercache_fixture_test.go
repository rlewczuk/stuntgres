package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var bcNames = []string{"bufferid", "relfilenode", "reltablespace", "reldatabase", "relforknumber", "relblocknumber", "isdirty", "usagecount", "pinning_backends"}
var bcTypes = []string{"INT4", "OID", "OID", "OID", "INT2", "INT8", "BOOL", "INT2", "INT4"}
var bcSQLTypes = []string{"integer", "oid", "oid", "oid", "smallint", "bigint", "boolean", "smallint", "integer"}

func bcFrom(function string, names, types []string) string {
	defs := make([]string, len(names))
	for i := range names {
		defs[i] = names[i] + " " + types[i]
	}
	return function + "() AS p(" + strings.Join(defs, ",") + ")"
}

func bcFunction() string { return bcFrom("bc_part.pg_buffercache_pages", bcNames, bcSQLTypes) }

func bcScenario(t *testing.T, install bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "bc_part")
	fixture.SchemaReferenceCleanup(t, "bc_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP EXTENSION IF EXISTS pg_buffercache CASCADE", "DROP SCHEMA IF EXISTS bc_part CASCADE", "DROP SCHEMA IF EXISTS bc_moved CASCADE"} {
						if _, err := c.ExecContext(ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			bcRun(t, p, b, c, fixture.Exec("CREATE SCHEMA bc_part", 0))
			if install {
				bcRun(t, p, b, c, fixture.Exec("CREATE EXTENSION pg_buffercache SCHEMA bc_part", 0))
			}
			body(t, p, b, c)
		})
	}
}

func bcRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func bcBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	bcRun(t, p, b, c, s)
	bcRun(t, p, b, c, fixture.Prepared(s))
}

func bcDDL(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, queries ...string) {
	t.Helper()
	for _, q := range queries {
		bcRun(t, p, b, c, fixture.Exec(q, 0))
	}
}

func bcError(query, code, message string) fixture.Step {
	s := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic(code, message))
	s.SelectRows = true
	return s
}

func bcFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step, stage string) {
	t.Helper()
	s.ErrorStage = "execute"
	bcRun(t, p, b, c, s)
	s.Prepared = true
	s.ErrorStage = stage
	bcRun(t, p, b, c, s)
}

func bcMetadata(t *testing.T, rows *sql.Rows, pg, view bool, names, types []string) {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil || len(cols) != len(names) {
		t.Fatal("column descriptor width", cols, err)
	}
	for i, col := range cols {
		if col.Name() != names[i] || col.DatabaseTypeName() != types[i] {
			t.Fatalf("column %d = %s/%s; want %s/%s", i, col.Name(), col.DatabaseTypeName(), names[i], types[i])
		}
		nullable, known := col.Nullable()
		wantNullable, wantKnown := view && !pg, view && !pg
		if nullable != wantNullable || known != wantKnown {
			t.Fatalf("column %s Nullable = %v,%v; want %v,%v", col.Name(), nullable, known, wantNullable, wantKnown)
		}
		if n, known := col.Length(); n != 0 || known {
			t.Fatal("unexpected length", col.Name(), n, known)
		}
		if precision, scale, known := col.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("unexpected decimal metadata", col.Name(), precision, scale, known)
		}
		scan := reflect.TypeFor[int64]()
		switch types[i] {
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		case "INT2":
			if pg {
				scan = reflect.TypeFor[int16]()
			}
		case "INT4":
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		case "OID":
			if pg {
				scan = reflect.TypeFor[any]()
			}
		}
		if col.ScanType() != scan {
			t.Fatal("ScanType", col.Name(), col.ScanType(), scan)
		}
	}
}

func bcNumber(t *testing.T, v any, pg bool) int64 {
	t.Helper()
	if n, ok := v.(int64); ok {
		return n
	}
	if pg {
		s, ok := v.(string)
		if bytes, bytesOK := v.([]byte); bytesOK {
			s, ok = string(bytes), true
		}
		if ok {
			n, err := strconv.ParseInt(s, 10, 64)
			if err == nil {
				return n
			}
		}
	}
	t.Fatalf("non-integer public cell %T(%v)", v, v)
	return 0
}

// Capture a detached, typed bag. Physical counters never become cross-engine
// goldens; callers independently assert the applicable model and identity.
func bcRows(t *testing.T, rows *sql.Rows, pg, view bool, names, types []string) [][]any {
	t.Helper()
	defer rows.Close()
	bcMetadata(t, rows, pg, view, names, types)
	var result [][]any
	for rows.Next() {
		row := make([]any, len(names))
		dest := make([]any, len(names))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, v := range row {
			if v == nil {
				continue
			}
			if types[i] == "BOOL" {
				if _, ok := v.(bool); !ok {
					t.Fatalf("non-bool cell %T", v)
				}
			} else {
				row[i] = bcNumber(t, v, pg)
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	return result
}

func bcQuery(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string, view bool, args ...any) [][]any {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, query, args...)
	if err != nil {
		t.Fatal(query, err)
	}
	return bcRows(t, rows, b.PG, view, bcNames, bcTypes)
}

func bcCapacity(t *testing.T, p *fixture.Pair, c *sql.Conn) int64 {
	t.Helper()
	var capacity int64
	if err := c.QueryRowContext(p.Ctx, "SELECT setting::bigint FROM pg_settings WHERE name='shared_buffers'").Scan(&capacity); err != nil || capacity <= 0 {
		t.Fatal("raw shared_buffers block capacity", capacity, err)
	}
	return capacity
}

func bcStorage(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) (node, space, database int64) {
	t.Helper()
	var defaultSpace int64
	// These fixtures create only default-placement heaps. Resolve that actual
	// tablespace object rather than requiring database-wide placement metadata.
	q := "SELECT c.relfilenode::bigint,CASE WHEN c.reltablespace=0 THEN t.oid ELSE c.reltablespace END::bigint,d.oid::bigint,t.oid::bigint FROM pg_class c CROSS JOIN pg_database d CROSS JOIN pg_tablespace t WHERE c.oid=$1::regclass AND d.datname=current_database() AND t.spcname='pg_default'"
	if err := c.QueryRowContext(p.Ctx, q, name).Scan(&node, &space, &database, &defaultSpace); err != nil || node <= 0 || space <= 0 || database <= 0 || defaultSpace != 1663 {
		t.Fatal("genuine storage identity", name, node, space, database, "pg_default", defaultSpace, err)
	}
	return
}

func bcTarget(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) [][]any {
	t.Helper()
	node, space, database := bcStorage(t, p, c, name)
	return bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode=$1::oid AND reltablespace=$2::oid AND reldatabase=$3::oid", true, node, space, database)
}

func bcReadHeap(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string) {
	t.Helper()
	bcRun(t, p, b, c, fixture.Select(query, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
}

func bcUnused(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	rows := bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode IS NULL LIMIT 1", true)
	if len(rows) != 1 {
		t.Fatal("required unused-buffer NULL witness absent", rows)
	}
	if rows[0][0] == nil || rows[0][0].(int64) < 1 {
		t.Fatal("unused bufferid", rows)
	}
	for i := 1; i < 9; i++ {
		if rows[0][i] != nil {
			t.Fatalf("unused %s = %#v, want NULL", bcNames[i], rows[0][i])
		}
	}
}

func bcUsage(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string, native int64) {
	t.Helper()
	rows := bcTarget(t, p, b, c, table)
	if !b.PG && native == 0 {
		if len(rows) != 0 {
			t.Fatal("unread native storage occupied", table, rows)
		}
		return
	}
	if len(rows) == 0 || (!b.PG && len(rows) != 1) {
		t.Fatal("read storage occupancy", table, rows)
	}
	for _, row := range rows {
		for i, cell := range row {
			if cell == nil {
				t.Fatal("occupied NULL", bcNames[i], row)
			}
		}
		usage, pins := row[7].(int64), row[8].(int64)
		if usage < 0 || usage > 5 || pins < 0 {
			t.Fatal("counter domain", row)
		}
		if !b.PG && (usage != native || pins != 0 || row[4] != int64(0) || row[5] != int64(0) || row[6] != false) {
			t.Fatal("deterministic logical read-cache", table, row, "want usage", native)
		}
	}
}
