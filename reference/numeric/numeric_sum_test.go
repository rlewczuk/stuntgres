package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations are independently specified from the PG18 manual and REL_18_0.
// The first oracle round validates these contracts before native implementation.
func numericSumScenario(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
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

func numericSumBoth(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, conn, recorder, s)
	}
}

func numericSumMetadata(pg bool, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(columns) != len(types) {
			t.Fatalf("metadata columns: %d", len(columns))
		}
		for i, c := range columns {
			want := reflect.TypeOf(float64(0))
			// pq advertises binary32 metadata even though REAL text values scan as float64.
			if pg && types[i] == "FLOAT4" {
				want = reflect.TypeOf(float32(0))
			}
			if types[i] == "INT8" {
				want = reflect.TypeOf(int64(0))
			}
			if types[i] == "NUMERIC" {
				want = reflect.TypeOf("")
				if pg {
					want = reflect.TypeOf((*any)(nil)).Elem()
				}
			}
			if c.ScanType() != want {
				t.Errorf("column %d ScanType: %v want %v", i, c.ScanType(), want)
			}
			if length, ok := c.Length(); length != 0 || ok {
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
			if nullable, known := c.Nullable(); nullable != !pg || known != !pg {
				t.Errorf("column %d Nullable: %v/%v", i, nullable, known)
			}
		}
	}
}

func numericSumArithmetic(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE t(g INTEGER,s SMALLINT,i INTEGER,b BIGINT,n NUMERIC,r REAL,d DOUBLE PRECISION)", 0))
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO t VALUES (1,1,1,1,1,1,1),(1,NULL,NULL,NULL,NULL,NULL,NULL),(1,3,3,3,3,3,3),(2,NULL,NULL,NULL,NULL,NULL,NULL)", 4))
	s := fixture.Select("SELECT sum(s),sum(i),sum(b),sum(n),sum(r),sum(d) FROM t", []string{"sum", "sum", "sum", "sum", "sum", "sum"}, []string{"INT8", "INT8", "NUMERIC", "NUMERIC", "FLOAT4", "FLOAT8"}, [][]any{{int64(4), int64(4), "4", "4", float64(4), float64(4)}})
	s.CheckMetadata = numericSumMetadata(pg, s.Types)
	numericSumBoth(t, p, conn, recorder, s)
	numericSumBoth(t, p, conn, recorder, fixture.Select(`SELECT pg_catalog.sum(i),"sum"(r) FROM t`, []string{"sum", "sum"}, []string{"INT8", "FLOAT4"}, [][]any{{int64(4), float64(4)}}))
	numericSumBoth(t, p, conn, recorder, fixture.Select("SELECT sum(1)", []string{"sum"}, []string{"INT8"}, [][]any{{int64(1)}}))
}

func TestReferenceNumericSumArithmetic(t *testing.T) { numericSumScenario(t, numericSumArithmetic) }

func TestReferenceNumericSumEmptyAndMetadata(t *testing.T) {
	numericSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		numericSumArithmetic(t, p, conn, recorder, pg)
		for _, suffix := range []string{" WHERE FALSE", " WHERE i IS NULL", " WHERE FALSE GROUP BY g"} {
			want := [][]any{{nil, nil, nil, nil, nil, nil}}
			if suffix == " WHERE FALSE GROUP BY g" {
				want = nil
			}
			s := fixture.Select("SELECT sum(s),sum(i),sum(b),sum(n),sum(r),sum(d) FROM t"+suffix, []string{"sum", "sum", "sum", "sum", "sum", "sum"}, []string{"INT8", "INT8", "NUMERIC", "NUMERIC", "FLOAT4", "FLOAT8"}, want)
			s.CheckMetadata = numericSumMetadata(pg, s.Types)
			numericSumBoth(t, p, conn, recorder, s)
		}
		s := fixture.Select("SELECT g,sum(s),sum(i),sum(b),sum(n),sum(r),sum(d) FROM t GROUP BY g", []string{"g", "sum", "sum", "sum", "sum", "sum", "sum"}, []string{"INT4", "INT8", "INT8", "NUMERIC", "NUMERIC", "FLOAT4", "FLOAT8"}, [][]any{{int64(1), int64(4), int64(4), "4", "4", float64(4), float64(4)}, {int64(2), nil, nil, nil, nil, nil, nil}})
		s.Bag = true
		numericSumBoth(t, p, conn, recorder, s)
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE sized(n NUMERIC(8,2) NOT NULL,i INTEGER NOT NULL)", 0))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO sized VALUES (1,1),(3,3)", 2))
		s = fixture.Select("SELECT sum(n),sum(i) FROM sized", []string{"sum", "sum"}, []string{"NUMERIC", "INT8"}, [][]any{{"4.00", int64(4)}})
		s.CheckMetadata = numericSumMetadata(pg, s.Types)
		numericSumBoth(t, p, conn, recorder, s)
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DELETE FROM t", 4))
		numericSumBoth(t, p, conn, recorder, fixture.Select("SELECT sum(i) FROM t", []string{"sum"}, []string{"INT8"}, [][]any{{nil}}))
	})
}
