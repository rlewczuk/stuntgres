package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/ngrok/sqlmw"
	"math"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func numericSumControls(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
	run(fixture.Exec("CREATE TABLE saved(g INTEGER,s SMALLINT,i INTEGER,b BIGINT,n NUMERIC,r REAL,d DOUBLE PRECISION)", 0))
	run(fixture.Exec("INSERT INTO saved VALUES (1,1,1,9223372036854775807,1.20,1,1),(1,3,3,9223372036854775807,3.456,3,3),(2,NULL,NULL,NULL,'Infinity','Infinity','Infinity'),(3,NULL,NULL,NULL,'NaN','NaN','NaN'),(4,NULL,NULL,NULL,0.0000,'-0','-0')", 5))
	run(fixture.Exec("CREATE VIEW sum_view AS SELECT g,sum(s) AS s,sum(i) AS i,sum(b) AS b,sum(n) AS n,sum(r) AS r,sum(d) AS d FROM saved GROUP BY g", 0))
	want := fixture.Select("SELECT g,s,i,b,n,r,d FROM sum_view WHERE g=1", []string{"g", "s", "i", "b", "n", "r", "d"}, []string{"INT4", "INT8", "INT8", "NUMERIC", "NUMERIC", "FLOAT4", "FLOAT8"}, [][]any{{int64(1), int64(4), int64(4), "18446744073709551614", "4.656", float64(4), float64(4)}})
	assertSaved := func(x fixture.Executor, rec *fixture.Recorder, isPG bool) {
		numericSumBoth(t, p, x, rec, want)
		numericSumBoth(t, p, x, rec, fixture.Select("SELECT n FROM sum_view WHERE g=2", []string{"n"}, []string{"NUMERIC"}, [][]any{{"Infinity"}}))
		numericSumBoth(t, p, x, rec, fixture.Select("SELECT n FROM sum_view WHERE g=3", []string{"n"}, []string{"NUMERIC"}, [][]any{{"NaN"}}))
		numericSumBoth(t, p, x, rec, fixture.Select("SELECT n FROM sum_view WHERE g=4", []string{"n"}, []string{"NUMERIC"}, [][]any{{"0.0000"}}))
		// This helper requires a Conn, which also keeps replay metadata on one session.
		c, ok := x.(*sql.Conn)
		if !ok {
			t.Fatal("control assertions require connection")
		}
		for _, column := range []struct{ name, typ string }{{"r", "FLOAT4"}, {"d", "FLOAT8"}} {
			numericSumFloatResult(t, p, c, isPG, "SELECT "+column.name+" AS sum FROM sum_view WHERE g=2", column.typ, math.Inf(1), 0)
			numericSumFloatResult(t, p, c, isPG, "SELECT "+column.name+" AS sum FROM sum_view WHERE g=3", column.typ, math.NaN(), 0)
			numericSumFloatResult(t, p, c, isPG, "SELECT "+column.name+" AS sum FROM sum_view WHERE g=4", column.typ, math.Copysign(0, -1), 0)
		}
	}
	assertSaved(conn, recorder, pg)
	run(fixture.Exec("INSERT INTO saved VALUES (1,8,8,1,0.0000,8,8)", 1))
	numericSumBoth(t, p, conn, recorder, fixture.Select("SELECT s,i,b,n,r,d FROM sum_view WHERE g=1", []string{"s", "i", "b", "n", "r", "d"}, []string{"INT8", "INT8", "NUMERIC", "NUMERIC", "FLOAT4", "FLOAT8"}, [][]any{{int64(12), int64(12), "18446744073709551615", "4.6560", float64(12), float64(12)}}))
	run(fixture.Exec("DELETE FROM saved WHERE s=8", 1))
	if pg {
		return
	} // No fictional PostgreSQL Reset/Dump/Restore API.
	q := "SELECT sum(s),sum(i),sum(b),sum(n),sum(r),sum(d) FROM saved WHERE g=1"
	held, err := conn.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	types := []string{"INT8", "INT8", "NUMERIC", "NUMERIC", "FLOAT4", "FLOAT8"}
	numericSumMetadata(false, types)(t, held)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	assertSaved(conn, recorder, false)
	numericSumMetadata(false, types)(t, held)
	if !held.Next() {
		t.Fatal("retained row missing", held.Err())
	}
	var s, i int64
	var b, n string
	var r, d float64
	if err := held.Scan(&s, &i, &b, &n, &r, &d); err != nil {
		t.Fatal(err)
	}
	if s != 4 || i != 4 || b != "18446744073709551614" || n != "4.656" || r != 4 || d != 4 {
		t.Fatalf("retained row: %v %v %v %v %v %v", s, i, b, n, r, d)
	}
	if held.Next() || held.Err() != nil {
		t.Fatal("retained completion", held.Err())
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/sum-copy")
	if err != nil {
		t.Fatal(err)
	}
	clone := sql.OpenDB(connector)
	defer clone.Close()
	if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{connector}, fixture.Interceptor{})
	recordingConnector, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	recordingClone := sql.OpenDB(recordingConnector)
	defer recordingClone.Close()
	copyConn, err := recordingClone.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer copyConn.Close()
	assertSaved(copyConn, &fixture.Recorder{RawBackend: fixture.RawNative}, false)
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("actual emitted SQL replay: %v\n%s", err, dump.String())
		}
		pgConn, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertSaved(pgConn, p.PostgresRecord, true)
		if err := pgConn.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceNumericSumControls(t *testing.T) { numericSumScenario(t, numericSumControls) }

func TestReferenceNumericSumCancellation(t *testing.T) {
	numericSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE t(v INTEGER)", 0))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO t VALUES (1),(3)", 2))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE destination(v BIGINT)", 0))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO destination VALUES (99)", 1))
		numericSumBoth(t, p, conn, recorder, fixture.Select("SELECT sum(v) FROM t", []string{"sum"}, []string{"INT8"}, [][]any{{int64(4)}}))
		if pg {
			return
		} // No timing/race claim about pq cancellation on a borrowed Conn.
		for _, prepared := range []bool{false, true} {
			for _, queryMode := range []bool{false, true} {
				q := "INSERT INTO destination SELECT sum(v) FROM t"
				if queryMode {
					q = "SELECT sum(v) FROM t"
				}
				var stmt *sql.Stmt
				var err error
				if prepared {
					stmt, err = conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				if queryMode {
					var rows *sql.Rows
					if prepared {
						rows, err = stmt.QueryContext(ctx)
					} else {
						rows, err = conn.QueryContext(ctx, q)
					}
					if rows != nil {
						rows.Close()
						t.Fatal("canceled SUM published rows")
					}
				} else {
					var result sql.Result
					if prepared {
						result, err = stmt.ExecContext(ctx)
					} else {
						result, err = conn.ExecContext(ctx, q)
					}
					if result != nil {
						t.Fatal("canceled SUM published result")
					}
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("ordinary cancellation: %v", err)
				}
				if _, ok := err.(interface{ SQLState() string }); ok {
					t.Fatalf("ordinary cancellation gained SQLSTATE: %v", err)
				}
			}
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Select("SELECT v FROM destination", []string{"v"}, []string{"INT8"}, [][]any{{int64(99)}}))
		numericSumBoth(t, p, conn, recorder, fixture.Select("SELECT sum(v) FROM t", []string{"sum"}, []string{"INT8"}, [][]any{{int64(4)}}))
	})
}
