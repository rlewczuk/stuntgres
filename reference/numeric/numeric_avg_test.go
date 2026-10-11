package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations in this family are independently specified from PostgreSQL 18
// documentation and REL_18_0 sources; the pinned oracle validates them.
func numericAvgScenario(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, conn, backend.Recorder, backend.PG)
		})
	}
}

func numericAvgBoth(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, conn, recorder, s)
	}
}

func numericAvgMetadata(pg bool, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(columns) != len(types) {
			t.Fatalf("metadata columns: %d", len(columns))
		}
		for i, c := range columns {
			wantScan := reflect.TypeOf(float64(0))
			if types[i] == "NUMERIC" {
				wantScan = reflect.TypeOf("")
				if pg {
					wantScan = reflect.TypeOf((*any)(nil)).Elem()
				}
			}
			if c.ScanType() != wantScan {
				t.Errorf("column %d ScanType: %v want %v", i, c.ScanType(), wantScan)
			}
			if length, ok := c.Length(); ok || length != 0 {
				t.Errorf("column %d Length: %d/%v", i, length, ok)
			}
			precision, scale, ok := c.DecimalSize()
			if pg && types[i] == "NUMERIC" {
				if precision != 65535 || scale != 65531 || !ok {
					t.Errorf("pq NUMERIC DecimalSize: %d/%d/%v", precision, scale, ok)
				}
			} else if precision != 0 || scale != 0 || ok {
				t.Errorf("column %d DecimalSize: %d/%d/%v", i, precision, scale, ok)
			}
			nullable, known := c.Nullable()
			if nullable != !pg || known != !pg {
				t.Errorf("column %d Nullable: %v/%v", i, nullable, known)
			}
		}
	}
}

func numericAvgArithmetic(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
	run(fixture.Exec("CREATE TABLE t(g INTEGER,s SMALLINT,i INTEGER,b BIGINT,n NUMERIC,r REAL,d DOUBLE PRECISION)", 0))
	run(fixture.Exec("INSERT INTO t VALUES (1,1,1,1,1,1,1),(1,NULL,NULL,NULL,NULL,NULL,NULL),(1,3,3,3,3,3,3)", 3))
	columns := []string{"avg", "avg", "avg", "avg", "avg", "avg"}
	types := []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "FLOAT8", "FLOAT8"}
	q := "SELECT avg(s),avg(i),avg(b),avg(n),avg(r),avg(d) FROM t"
	for _, tc := range []struct {
		suffix string
		rows   [][]any
	}{
		{"", [][]any{{"2.0000000000000000", "2.0000000000000000", "2.0000000000000000", "2.0000000000000000", float64(2), float64(2)}}},
		{" WHERE FALSE", [][]any{{nil, nil, nil, nil, nil, nil}}},
		{" WHERE i IS NULL", [][]any{{nil, nil, nil, nil, nil, nil}}},
		{" WHERE FALSE GROUP BY g", nil},
	} {
		s := fixture.Select(q+tc.suffix, columns, types, tc.rows)
		s.CheckMetadata = numericAvgMetadata(pg, types)
		numericAvgBoth(t, p, conn, recorder, s)
	}
	numericAvgBoth(t, p, conn, recorder, fixture.Select(`SELECT pg_catalog.avg(i) AS mean,"avg"(r) AS widened FROM t`, []string{"mean", "widened"}, []string{"NUMERIC", "FLOAT8"}, [][]any{{"2.0000000000000000", float64(2)}}))
	run(fixture.Exec("CREATE TABLE sized(n NUMERIC(8,2) NOT NULL)", 0))
	run(fixture.Exec("INSERT INTO sized VALUES (1),(3)", 2))
	s := fixture.Select("SELECT avg(n) AS mean FROM sized", []string{"mean"}, []string{"NUMERIC"}, [][]any{{"2.0000000000000000"}})
	s.CheckMetadata = numericAvgMetadata(pg, s.Types)
	numericAvgBoth(t, p, conn, recorder, s)
}

func TestReferenceNumericAvgArithmetic(t *testing.T) {
	numericAvgScenario(t, numericAvgArithmetic)
}
