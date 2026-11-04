package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGLSNDiffValues(t *testing.T) {
	// Expected decimal integers follow unsigned 64-bit subtraction, independently
	// of either backend and without a floating-point or signed-width intermediate.
	vectors := [][3]string{
		{"0/0", "0/0", "0"}, {"0/1", "0/0", "1"}, {"0/0", "0/1", "-1"},
		{"1/0", "0/0", "4294967296"}, {"0/0", "1/0", "-4294967296"},
		{"1/0", "0/FFFFFFFF", "1"}, {"80000000/0", "7FFFFFFF/FFFFFFFF", "1"},
		{"FFFFFFFF/FFFFFFFF", "0/0", "18446744073709551615"},
		{"0/0", "FFFFFFFF/FFFFFFFF", "-18446744073709551615"},
		{"FFFFFFFF/FFFFFFFF", "FFFFFFFF/FFFFFFFF", "0"},
	}
	pgLSNScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE pglsn_diff_values(id integer,a pg_lsn,b pg_lsn)", 0))
		var columns [][]any
		for _, v := range vectors {
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{
					"SELECT pg_wal_lsn_diff(" + tidQuote(v[0]) + "," + tidQuote(v[1]) + ") AS difference",
					"SELECT pg_catalog.pg_wal_lsn_diff(" + tidQuote(strings.ToLower(v[0])) + "::pg_lsn," + tidQuote(strings.ToLower(v[1])) + "::pg_lsn) AS difference",
				} {
					s := fixture.Select(q, []string{"difference"}, []string{"NUMERIC"}, [][]any{{v[2]}})
					s.Prepared = prepared
					run(s)
				}
				s := fixture.Select("SELECT pg_wal_lsn_diff($1,$2) AS difference", []string{"difference"}, []string{"NUMERIC"}, [][]any{{v[2]}}, v[0], v[1])
				s.Prepared = prepared
				run(s)
			}
			columns = append(columns, []any{v[2]})
		}
		// Use fixed integer IDs for the independently ordered column witness.
		run(fixture.Exec("INSERT INTO pglsn_diff_values VALUES(0,'0/0','0/0'),(1,'0/1','0/0'),(2,'0/0','0/1'),(3,'1/0','0/0'),(4,'0/0','1/0'),(5,'1/0','0/FFFFFFFF'),(6,'80000000/0','7FFFFFFF/FFFFFFFF'),(7,'FFFFFFFF/FFFFFFFF','0/0'),(8,'0/0','FFFFFFFF/FFFFFFFF'),(9,'FFFFFFFF/FFFFFFFF','FFFFFFFF/FFFFFFFF'),(10,NULL,'0/0'),(11,'0/0',NULL),(12,NULL,NULL)", 13))
		columns = append(columns, []any{nil}, []any{nil}, []any{nil})
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT pg_wal_lsn_diff(a,b) AS difference FROM pglsn_diff_values ORDER BY id", []string{"difference"}, []string{"NUMERIC"}, columns)
			s.Prepared = prepared
			run(s)
			for _, call := range []string{"NULL,'0/0'", "'0/0',NULL", "NULL,NULL"} {
				s := fixture.Select("SELECT pg_wal_lsn_diff("+call+") AS difference", []string{"difference"}, []string{"NUMERIC"}, [][]any{{nil}})
				s.Prepared = prepared
				run(s)
			}
		}
	})
}

func pgLSNDiffMetadata(t *testing.T, rows *sql.Rows, pg bool, name string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 1 {
		t.Fatal(columns, err)
	}
	c := columns[0]
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if c.Name() != name || c.DatabaseTypeName() != "NUMERIC" || c.ScanType() != scan {
		t.Fatalf("difference metadata: %s/%s/%v", c.Name(), c.DatabaseTypeName(), c.ScanType())
	}
	if _, known := c.Length(); known {
		t.Fatal("unexpected computed numeric length")
	}
	if nullable, known := c.Nullable(); nullable || known {
		t.Fatal("unexpected computed numeric nullability", nullable, known)
	}
	precision, scale, known := c.DecimalSize()
	if pg {
		// lib/pq's unconstrained NUMERIC decoding convention, not a typmod.
		if precision != 65535 || scale != 65531 || !known {
			t.Fatal("pq numeric precision convention", precision, scale, known)
		}
	} else if precision != 0 || scale != 0 || known {
		t.Fatal("native unconstrained numeric precision", precision, scale, known)
	}
}

func TestReferencePGLSNDiffMetadata(t *testing.T) {
	pgLSNScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE pglsn_diff_metadata(id integer,a pg_lsn,b pg_lsn)", 0))
		run(fixture.Exec("INSERT INTO pglsn_diff_metadata VALUES(1,'FFFFFFFF/FFFFFFFF','0/0'),(2,NULL,'0/0')", 2))
		for _, prepared := range []bool{false, true} {
			for _, alias := range []string{"", " AS difference"} {
				name := "pg_wal_lsn_diff"
				if alias != "" {
					name = "difference"
				}
				for _, empty := range []bool{false, true} {
					q := "SELECT pg_wal_lsn_diff(a,b)" + alias + " FROM pglsn_diff_metadata"
					want := [][]any{{"18446744073709551615"}, {nil}}
					if empty {
						q += " WHERE FALSE"
						want = nil
					}
					q += " ORDER BY id"
					s := fixture.Select(q, []string{name}, []string{"NUMERIC"}, want)
					s.Prepared = prepared
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgLSNDiffMetadata(t, rows, b.PG, name) }
					s.CheckScan = func(t *testing.T, rows *sql.Rows, i int) {
						var raw any
						if err := rows.Scan(&raw); err != nil {
							t.Fatal(err)
						}
						if want[i][0] == nil {
							if raw != nil {
								t.Fatal("raw numeric NULL", raw)
							}
						} else if b.PG {
							if value, ok := raw.([]byte); !ok || string(value) != want[i][0] {
								t.Fatalf("raw pq numeric: %T %v", raw, raw)
							}
						} else if value, ok := raw.(string); !ok || value != want[i][0] {
							t.Fatalf("raw native numeric: %T %v", raw, raw)
						}
					}
					run(s)
				}
			}
		}
	})
}

func TestReferencePGLSNDiffFeatureSmoke(t *testing.T) {
	pgLSNScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		s := fixture.Select("SELECT pg_wal_lsn_diff('FFFFFFFF/FFFFFFFF','0/0')", []string{"pg_wal_lsn_diff"}, []string{"NUMERIC"}, [][]any{{"18446744073709551615"}})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgLSNDiffMetadata(t, rows, b.PG, "pg_wal_lsn_diff") }
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		q := "SELECT pg_wal_lsn_diff($1,$2) AS difference"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, frame := range []struct{ a, b, want any }{
			{"FFFFFFFF/FFFFFFFF", "0/0", "18446744073709551615"},
			{"0/0", "FFFFFFFF/FFFFFFFF", "-18446744073709551615"},
			{"FFFFFFFF/FFFFFFFF", "FFFFFFFF/FFFFFFFF", "0"},
			{nil, "0/0", nil}, {"0/0", nil, nil}, {nil, nil, nil},
		} {
			s := fixture.Select(q, []string{"difference"}, []string{"NUMERIC"}, [][]any{{frame.want}}, frame.a, frame.b)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgLSNDiffMetadata(t, rows, b.PG, "difference") }
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		d := pgLSNDiagnostic("bad")
		d.Context = "unnamed portal parameter $1 = '...'"
		bad := fixture.ErrorDiagnostic(q, d, "bad", "0/0")
		bad.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"difference"}, []string{"NUMERIC"}, [][]any{{"1"}}, "0/1", "0/0"))
	})
}
