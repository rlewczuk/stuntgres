package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestReferenceSizePrepared(t *testing.T) {
	sizeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, v := range []struct {
			q, typ  string
			values  []any
			results []any
		}{
			{"SELECT pg_size_bytes($1)", "INT8", []any{"1 MB", []byte("2 kB"), nil, "-.5", "1 MB"}, []any{int64(1048576), int64(2048), nil, int64(-1), int64(1048576)}},
			{"SELECT pg_size_pretty($1::bigint)", "TEXT", []any{int64(1024), "10752", nil, int64(-10752), int64(1024)}, []any{"1024 bytes", "11 kB", nil, "-11 kB", "1024 bytes"}},
			{"SELECT pg_size_pretty($1::numeric)", "TEXT", []any{"10.5000", "11528652096115048448", nil, "NaN", "10.5000"}, []any{"10.5000 bytes", "10240 PB", nil, "NaN PB", "10.5000 bytes"}},
		} {
			t.Run(v.q, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.ctx, v.q)
				if err != nil {
					t.Fatal("valid Prepare", err)
				}
				defer stmt.Close()
				x := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: v.q}
				name := "pg_size_pretty"
				if v.typ == "INT8" {
					name = "pg_size_bytes"
				}
				for i, arg := range v.values {
					referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect(v.q, []string{name}, []string{v.typ}, [][]any{{v.results[i]}}, arg))
					if i == 0 {
						bad, d := "bad", sizeDiagnostic("22P02", `invalid input syntax for type bigint: "bad"`, "", "")
						if v.typ == "INT8" {
							bad, d = "1 KiB", sizeInvalid("1 KiB", "KiB")
						} else {
							if v.q == "SELECT pg_size_pretty($1::numeric)" {
								d.Message = `invalid input syntax for type numeric: "bad"`
							}
							d.Context = "unnamed portal parameter $1 = '...'"
						}
						s := referenceErrorDiagnostic(v.q, d, bad)
						s.selectRows, s.errorStage = true, "execute"
						referenceRunStep(t, p.ctx, x, b.recorder, s)
					}
				}
			})
		}
		sizeBoth(t, p, b, c, referenceSelect("SELECT pg_size_bytes($1),pg_size_bytes($1)", []string{"pg_size_bytes", "pg_size_bytes"}, []string{"INT8", "INT8"}, [][]any{{int64(1048576), int64(1048576)}}, "1 MB"), false)
	})
}

func sizeColumns(t *testing.T, rows *sql.Rows, names, types []string) {
	t.Helper()
	ct, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	sizeColumnMetadata(t, ct, names, types, false, false)
}

func sizeColumnMetadata(t *testing.T, ct []*sql.ColumnType, names, types []string, nullable, known bool) {
	t.Helper()
	if len(ct) != len(names) {
		t.Fatal("column width", len(ct), names)
	}
	for i, col := range ct {
		scan, length, hasLength := reflect.TypeFor[int64](), int64(0), false
		if types[i] == "TEXT" {
			scan, length, hasLength = reflect.TypeFor[string](), 9223372036854775807, true
		}
		l, lk := col.Length()
		n, nk := col.Nullable()
		precision, scale, dk := col.DecimalSize()
		if col.Name() != names[i] || col.DatabaseTypeName() != types[i] || col.ScanType() != scan || l != length || lk != hasLength || n != nullable || nk != known || precision != 0 || scale != 0 || dk {
			t.Fatalf("column %d: %s %s scan=%v length=%d/%v nullable=%v/%v decimal=%d,%d/%v", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), l, lk, n, nk, precision, scale, dk)
		}
	}
}

func TestReferenceSizeMetadata(t *testing.T) {
	sizeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE size_empty(s text,i bigint,n numeric(8,4))", 0))
		for _, s := range []referenceStep{
			referenceSelect("SELECT pg_size_bytes(NULL),pg_size_pretty(NULL::bigint),pg_size_pretty(NULL::numeric)", []string{"pg_size_bytes", "pg_size_pretty", "pg_size_pretty"}, []string{"INT8", "TEXT", "TEXT"}, [][]any{{nil, nil, nil}}),
			referenceSelect("SELECT pg_size_bytes(s),pg_size_pretty(i),pg_size_pretty(n) FROM size_empty", []string{"pg_size_bytes", "pg_size_pretty", "pg_size_pretty"}, []string{"INT8", "TEXT", "TEXT"}, nil),
			referenceSelect("SELECT pg_size_bytes('1 MB'),pg_size_pretty(1024::bigint),pg_size_pretty(10.5000::numeric(8,4))", []string{"pg_size_bytes", "pg_size_pretty", "pg_size_pretty"}, []string{"INT8", "TEXT", "TEXT"}, [][]any{{int64(1048576), "1024 bytes", "10.5000 bytes"}}),
		} {
			s.checkMetadata = func(t *testing.T, rows *sql.Rows) { sizeColumns(t, rows, s.columns, s.types) }
			s.checkScan = func(t *testing.T, rows *sql.Rows, index int) {
				var bytes, big, num any
				if err := rows.Scan(&bytes, &big, &num); err != nil {
					t.Fatal(err)
				}
				for i, v := range []any{bytes, big, num} {
					referenceAssertRawValue(t, b.pg, s.types[i], v)
				}
				if bytes != nil {
					var n int64
					var pretty, numeric string
					if err := rows.Scan(&n, &pretty, &numeric); err != nil {
						t.Fatal(err)
					}
					if n != 1048576 || pretty != "1024 bytes" || numeric != "10.5000 bytes" {
						t.Fatal(n, pretty, numeric)
					}
				}
			}
			sizeBoth(t, p, b, c, s, false)
		}
	})
}

func TestReferenceSizeCancellation(t *testing.T) {
	sizeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		q := "SELECT pg_size_bytes($1),pg_size_pretty($2::numeric)"
		stmt, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			if prepared {
				rows, err = stmt.QueryContext(ctx, "1 MB", "10.5000")
			} else {
				rows, err = b.db.QueryContext(ctx, q, "1 MB", "10.5000")
			}
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary cancellation", err)
			}
		}
		s := referenceSelect(q, []string{"pg_size_bytes", "pg_size_pretty"}, []string{"INT8", "TEXT"}, [][]any{{int64(1048576), "10.5000 bytes"}}, "1 MB", "10.5000")
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}, b.recorder, s)
	})
}
