package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var fsNames = []string{"blkno", "avail"}
var fsTypes = []string{"INT8", "INT2"}

func fsScenario(t *testing.T, schema string, install bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	for _, name := range []string{schema, "fs_moved"} {
		fixture.SchemaReferenceCleanup(t, name)
	}
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
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS pg_freespacemap CASCADE", "DROP SCHEMA IF EXISTS " + schema + " CASCADE", "DROP SCHEMA IF EXISTS fs_moved CASCADE"} {
						if _, err := c.ExecContext(ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			fsRun(t, p, b, c, fixture.Exec("CREATE SCHEMA "+schema, 0))
			if install {
				fsRun(t, p, b, c, fixture.Exec("CREATE EXTENSION pg_freespacemap SCHEMA "+schema, 0))
			}
			body(t, p, b, c)
		})
	}
}

func fsCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fsScenario(t, "fs_part", true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fsRun(t, p, b, c, fixture.Exec("CREATE TABLE fs_part.heap(k integer)", 0))
		fsRun(t, p, b, c, fixture.Exec("ALTER TABLE fs_part.heap SET(autovacuum_enabled=false)", 0))
		body(t, p, b, c)
	})
}

func fsRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func fsBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fsRun(t, p, b, c, s)
	fsRun(t, p, b, c, fixture.Prepared(s))
}

func fsError(q, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.ControlsReferenceDiagnostic(code, message), args...)
	s.SelectRows = true
	return s
}

func fsSignature(q, signature string) fixture.Step {
	s := fsError(q, "42883", "function "+signature+" does not exist")
	s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	s.Diagnostic.Position = "15"
	return s
}

func fsMetadata(s fixture.Step, pg bool) fixture.Step {
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
			if n, known := col.Nullable(); n || known {
				t.Fatal("computed nullability", n, known)
			}
			if n, known := col.Length(); n != 0 || known {
				t.Fatal("length", n, known)
			}
			if precision, scale, known := col.DecimalSize(); precision != 0 || scale != 0 || known {
				t.Fatal("precision", precision, scale, known)
			}
			scan := reflect.TypeFor[int64]()
			if pg && s.Types[i] == "INT2" {
				scan = reflect.TypeFor[int16]()
			}
			if col.ScanType() != scan {
				t.Fatal("integer ScanType", col.ScanType(), scan)
			}
		}
	}
	return s
}

func fsProbe(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, target any, block int64, avail any) {
	t.Helper()
	fsBoth(t, p, b, c, fsMetadata(fixture.Select("SELECT fs_part.pg_freespace($1::regclass,$2::bigint) AS avail", []string{"avail"}, []string{"INT2"}, [][]any{{avail}}, target, block), b.PG))
}

// Native has a compact visible-live-row domain, independent of relation_size's
// retained-physical-version byte counter. PostgreSQL instead uses its real page
// domain and FSM values, whose freshness is deliberately not an expectation.
func fsCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, target string, native []int64) {
	t.Helper()
	var expected [][]any
	if b.PG {
		var bytes, blockSize int64
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_relation_size($1::regclass),current_setting('block_size')::bigint", target).Scan(&bytes, &blockSize); err != nil {
			t.Fatal(err)
		}
		if blockSize != 8192 || bytes < 0 || bytes%8192 != 0 {
			t.Fatal("physical page counters", bytes, blockSize)
		}
		rows, err := c.QueryContext(p.Ctx, "SELECT * FROM fs_part.pg_freespace($1::regclass) ORDER BY blkno", target)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var block, avail int64
			if err := rows.Scan(&block, &avail); err != nil {
				t.Fatal(err)
			}
			if block != int64(len(expected)) || avail < 0 || avail > 8160 || avail%32 != 0 {
				t.Fatal("FSM domain/quantization", block, avail)
			}
			expected = append(expected, []any{block, avail})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if int64(len(expected)) != bytes/8192 {
			t.Fatal("FSM physical domain", len(expected), bytes)
		}
	} else {
		for block, avail := range native {
			expected = append(expected, []any{int64(block), avail})
		}
	}
	fsBoth(t, p, b, c, fsMetadata(fixture.Select("SELECT * FROM fs_part.pg_freespace($1::regclass) ORDER BY blkno", fsNames, fsTypes, expected, target), b.PG))
	for _, row := range expected {
		fsProbe(t, p, b, c, target, row[0].(int64), row[1])
	}
	fsProbe(t, p, b, c, target, int64(len(expected))+1, int64(0))
	fsProbe(t, p, b, c, target, 4294967294, int64(0))
}
func fsEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, target any) {
	t.Helper()
	fsBoth(t, p, b, c, fsMetadata(fixture.Select("SELECT * FROM fs_part.pg_freespace($1::regclass) ORDER BY blkno", fsNames, fsTypes, nil, target), b.PG))
}

func fsOID(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT $1::regclass::oid", name).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 {
		t.Fatal("invalid generated relation identity", oid)
	}
	return oid
}

func fsDependency(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, scalar, wrapper string) {
	t.Helper()
	q := "SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=$1::regprocedure AND refclassid='pg_proc'::regclass AND refobjid=$2::regprocedure AND deptype='n'"
	fsBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, wrapper, scalar))
}

func fsBodies(schema string) fixture.Step {
	q := "SELECT p.pronargs::integer AS nargs,l.lanname::text AS language,p.prosrc AS source,p.probin AS binary,p.prosqlbody IS NOT NULL AS body,p.proisstrict AS strict FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='" + schema + "' AND p.proname='pg_freespace'"
	s := fixture.Select(q, []string{"nargs", "language", "source", "binary", "body", "strict"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{
		{int64(1), "sql", "", nil, true, false},
		{int64(2), "c", "pg_freespace", "$libdir/pg_freespacemap", false, true},
	})
	s.Bag = true
	return s
}
